package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/agent/internal/traffic"
	"github.com/cuihairu/ferry/packages/agentproto"
)

func TestLoadWatch(t *testing.T) {
	base := time.Now()
	var w loadWatch

	// 阈值以下不报
	if w.observe(10, base.Add(1*time.Minute)) {
		t.Fatal("low cpu should not fire")
	}
	// 第一次超阈值只是累计，第二次才报（此时 last 为零值，立即生效）
	if w.observe(95, base.Add(2*time.Minute)) {
		t.Fatal("single spike should not fire")
	}
	if !w.observe(95, base.Add(3*time.Minute)) {
		t.Fatal("sustained load should fire")
	}
	// 冷却期内持续高负载不重报（streak 持续累计）
	if w.observe(95, base.Add(4*time.Minute)) || w.observe(95, base.Add(10*time.Minute)) {
		t.Fatal("cooldown should suppress")
	}
	// 冷却期（30m）过后再报：上一次为 +3m，+34m 已过
	if !w.observe(95, base.Add(34*time.Minute)) {
		t.Fatal("after cooldown should fire again")
	}
	// 回落清零：再升需重新累计两次，且新冷却期未过不报
	if w.observe(10, base.Add(35*time.Minute)) {
		t.Fatal("recovered cpu should reset streak")
	}
	if w.observe(99, base.Add(36*time.Minute)) {
		t.Fatal("fresh streak needs two samples")
	}
	if w.observe(99, base.Add(37*time.Minute)) {
		t.Fatal("fresh streak still in cooldown")
	}
	// 上一次报于 +34m，+65m 越过冷却期后可再报
	if !w.observe(99, base.Add(65*time.Minute)) {
		t.Fatal("third fire after cooldown")
	}
}

// newDispatchApp 造最小可分发 App：不接真实进程/探测/流量组件。
// version 对齐生产构造器 New(cfg, version) 的入参，握手断言要用。
func newDispatchApp() *App {
	return &App{
		log:        log.New(io.Discard, "", 0),
		version:    "v1.2.3-batch",
		interval:   30 * time.Second,
		certAlarms: map[string]time.Time{},
	}
}

// recorder 捕获 sendIfConnected 发出的应答。
type recorder struct {
	mu   sync.Mutex
	msgs []agentproto.Envelope
}

func (r *recorder) send(env agentproto.Envelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, env)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.msgs)
}

func (r *recorder) last(t *testing.T) agentproto.Envelope {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		t.Fatal("no message captured")
	}
	return r.msgs[len(r.msgs)-1]
}

func mustDecode(t *testing.T, env agentproto.Envelope, v any) {
	t.Helper()
	if err := env.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", env.Type, err)
	}
}

func TestOnMessageHelloAckDelivery(t *testing.T) {
	a := newDispatchApp()
	a.hello = make(chan agentproto.Envelope, 1)
	env, err := agentproto.NewEnvelope("h1", agentproto.MsgHelloAck, agentproto.HelloAck{})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	a.OnMessage(context.Background(), env, a.sendIfConnected)
	select {
	case got := <-a.hello:
		if got.ID != "h1" {
			t.Fatalf("delivered id = %q; want h1", got.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("hello ack not delivered")
	}
}

func TestOnMessageHelloAckNoWaiter(t *testing.T) {
	a := newDispatchApp() // hello 通道为 nil（未连接握手期）
	env, _ := agentproto.NewEnvelope("h1", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.OnMessage(context.Background(), env, a.sendIfConnected) // 不应 panic
}

func TestOnMessageHeartbeatAckInterval(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	env, _ := agentproto.NewEnvelope("hb", agentproto.MsgHeartbeatAck, agentproto.HeartbeatAck{NextIntervalSec: 45})
	a.OnMessage(context.Background(), env, a.sendIfConnected)
	if got := a.currentInterval(); got != 45*time.Second {
		t.Fatalf("interval = %v; want 45s", got)
	}
	// 零值 interval 不改当前周期
	env2, _ := agentproto.NewEnvelope("hb", agentproto.MsgHeartbeatAck, agentproto.HeartbeatAck{})
	a.OnMessage(context.Background(), env2, a.sendIfConnected)
	if got := a.currentInterval(); got != 45*time.Second {
		t.Fatalf("interval after zero ack = %v; want 45s", got)
	}
}

func TestOnMessageUpgradeRejected(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	// 载荷解码失败
	bad, _ := agentproto.NewEnvelope("u1", agentproto.MsgUpgrade, "junk")
	a.OnMessage(context.Background(), bad, a.sendIfConnected)
	// 缺 version/url
	missing, _ := agentproto.NewEnvelope("u2", agentproto.MsgUpgrade, agentproto.Upgrade{Version: "v2"})
	a.OnMessage(context.Background(), missing, a.sendIfConnected)

	if rec.count() != 2 {
		t.Fatalf("replies = %d; want 2", rec.count())
	}
	var ack1 agentproto.UpgradeAck
	mustDecode(t, rec.last(t), &ack1)
	if ack1.OK || ack1.Error == "" {
		t.Fatalf("bad payload ack = %+v; want rejected with error", ack1)
	}
	var ack2 agentproto.UpgradeAck
	mustDecode(t, rec.last(t), &ack2)
	if ack2.OK || ack2.Error != "version and url are required" {
		t.Fatalf("missing fields ack = %+v; want rejected", ack2)
	}
}

func TestOnMessageUpgradeAccepted(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	// 受理即回 ack；后台 doUpgrade 走不可达 URL 失败只记日志（不换二进制）
	env, _ := agentproto.NewEnvelope("u3", agentproto.MsgUpgrade,
		agentproto.Upgrade{Version: "v2", URL: "http://127.0.0.1:1/agent"})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	if rec.count() != 1 {
		t.Fatalf("replies = %d; want 1", rec.count())
	}
	var ack agentproto.UpgradeAck
	mustDecode(t, rec.last(t), &ack)
	if !ack.OK || ack.Version != "v2" || ack.Error != "" {
		t.Fatalf("ack = %+v; want accepted v2", ack)
	}
}

func TestOnMessageProcCtlUnknown(t *testing.T) {
	a := newDispatchApp()
	a.mgr = procs.New(nil, a.log)
	rec := &recorder{}
	a.setSend(rec.send)

	env, _ := agentproto.NewEnvelope("c1", agentproto.MsgProcCtl,
		agentproto.ProcCtl{Proc: "xray", Action: agentproto.ProcActionStart})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	if rec.count() != 1 {
		t.Fatalf("replies = %d; want 1", rec.count())
	}
	var ack agentproto.ProcCtlAck
	mustDecode(t, rec.last(t), &ack)
	if ack.OK || ack.Proc != "xray" || ack.Action != agentproto.ProcActionStart {
		t.Fatalf("ack = %+v; want rejected xray/start", ack)
	}
	if ack.Error == "" {
		t.Error("expected error for unknown proc")
	}
}

func TestOnMessageProcLogsUnknown(t *testing.T) {
	a := newDispatchApp()
	a.mgr = procs.New(nil, a.log)
	rec := &recorder{}
	a.setSend(rec.send)

	env, _ := agentproto.NewEnvelope("l1", agentproto.MsgProcLogs,
		agentproto.ProcLogsReq{Proc: "xray", Limit: 50})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	var ack agentproto.ProcLogsAck
	mustDecode(t, rec.last(t), &ack)
	if ack.Proc != "xray" || ack.Error == "" || ack.Lines != nil {
		t.Fatalf("ack = %+v; want error for unknown proc", ack)
	}
}

func TestOnMessageConfigPushUnknownProc(t *testing.T) {
	a := newDispatchApp() // 空 cfg.Procs → 任何 proc 都 unknown
	replies := make(chan agentproto.Envelope, 4)
	a.setSend(func(env agentproto.Envelope) error {
		replies <- env
		return nil
	})

	env, _ := agentproto.NewEnvelope("p1", agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: "ghost", Kind: "xray", Version: "v1", Payload: "{}"})
	a.OnMessage(context.Background(), env, a.sendIfConnected) // 异步执行

	select {
	case got := <-replies:
		var ack agentproto.ConfigAck
		mustDecode(t, got, &ack)
		if ack.OK || ack.Proc != "ghost" || ack.Version != "v1" {
			t.Fatalf("ack = %+v; want rejected ghost/v1", ack)
		}
		if ack.Error == "" {
			t.Error("expected unknown proc error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("config ack not delivered")
	}
}

func TestOnMessageUnknownType(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)
	env, _ := agentproto.NewEnvelope("x1", "bogus.type", map[string]string{})
	a.OnMessage(context.Background(), env, a.sendIfConnected)
	if rec.count() != 0 {
		t.Fatalf("unknown type should not reply; got %d", rec.count())
	}
}

func TestOnDisconnectedClearsSend(t *testing.T) {
	a := newDispatchApp()
	a.setSend((&recorder{}).send)
	// traf 生产构造器恒初始化（app.go:89），这里补真实实例供清空
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	a.OnDisconnected()
	if err := a.sendIfConnected(agentproto.Envelope{}); err == nil {
		t.Fatal("send after disconnect should fail")
	}
}

func TestMaybeCalibrateSkipsWithoutPlan(t *testing.T) {
	a := newDispatchApp() // Meta.BwDownMbps=0 → 不测速
	rec := &recorder{}
	a.maybeCalibrate(rec.send)
	a.maybeCalibrate(rec.send) // 二次调用走 calibrated 短路
	if rec.count() != 0 {
		t.Fatalf("calibrate without plan should not send; got %d", rec.count())
	}
}

func TestWatchAlarmsCertExpiryThrottle(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	now := time.Now()
	notAfter := now.Add(10 * 24 * time.Hour) // 30 天窗口内
	hb := agentproto.Heartbeat{Certs: []agentproto.CertStatus{{Domain: "a.com", NotAfter: notAfter}}, At: now}

	a.watchAlarms(hb)
	a.watchAlarms(hb) // 同证书同有效期只报一次
	if got := rec.count(); got != 1 {
		t.Fatalf("alarms = %d; want 1 (throttled)", got)
	}
	var al agentproto.Alarm
	mustDecode(t, rec.last(t), &al)
	if al.Kind != agentproto.AlarmKindCertExpiry {
		t.Fatalf("kind = %q; want cert expiry", al.Kind)
	}

	// 续期后（NotAfter 变化）重新具备告警资格
	hb.Certs[0].NotAfter = notAfter.Add(24 * time.Hour)
	a.watchAlarms(hb)
	if got := rec.count(); got != 2 {
		t.Fatalf("alarms after renew = %d; want 2", got)
	}

	// 远期证书不告警
	far := agentproto.Heartbeat{Certs: []agentproto.CertStatus{{Domain: "b.com", NotAfter: now.Add(365 * 24 * time.Hour)}}}
	a.watchAlarms(far)
	if got := rec.count(); got != 2 {
		t.Fatalf("far cert should not alarm; got %d", got)
	}
}

func TestWatchAlarmsHighLoadStreakAndCooldown(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	high := agentproto.Heartbeat{CPUUtil: 95}
	low := agentproto.Heartbeat{CPUUtil: 10}

	a.watchAlarms(high) // streak 1，不报
	if got := rec.count(); got != 0 {
		t.Fatalf("single spike should not alarm; got %d", got)
	}
	a.watchAlarms(high) // streak 2，报一次
	if got := rec.count(); got != 1 {
		t.Fatalf("sustained load should alarm; got %d", got)
	}
	var al agentproto.Alarm
	mustDecode(t, rec.last(t), &al)
	if al.Kind != agentproto.AlarmKindHighLoad {
		t.Fatalf("kind = %q; want high load", al.Kind)
	}

	a.watchAlarms(low) // 回落清 streak
	a.watchAlarms(high)
	if got := rec.count(); got != 1 {
		t.Fatalf("fresh streak needs two samples; got %d", got)
	}
	a.watchAlarms(high) // streak 2 但冷却期内（30m），抑制
	if got := rec.count(); got != 1 {
		t.Fatalf("cooldown should suppress; got %d", got)
	}
}

// TestNewWiresComponents 覆盖装配：空配置也能建全组件（mgr/cfgd/probe/trafs）
// 与心跳快照口、告警节流 map。
func TestNewWiresComponents(t *testing.T) {
	a := New(config.Config{}, "v1.2.3")
	if a.mgr == nil || a.cfgd == nil || a.probe == nil || a.traf == nil {
		t.Fatal("New must wire mgr/cfgd/probe/traf")
	}
	if a.Procs == nil {
		t.Fatal("New must wire Procs snapshot fn")
	}
	if a.certAlarms == nil {
		t.Fatal("New must init cert alarm throttle map")
	}
	if a.version != "v1.2.3" {
		t.Fatalf("version = %q; want v1.2.3", a.version)
	}
}

// TestReportStatusAndAlarm 覆盖进程状态/告警上报：连着发送口时封装
// proc.report 与 alarm 信封送出。
func TestReportStatusAndAlarm(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	a.reportStatus(agentproto.ProcStatus{Name: "xray", State: "running"})
	env := rec.last(t)
	if env.Type != agentproto.MsgProcReport {
		t.Fatalf("type = %q; want proc.report", env.Type)
	}
	var pr agentproto.ProcReport
	mustDecode(t, env, &pr)
	if len(pr.Procs) != 1 || pr.Procs[0].Name != "xray" || pr.Procs[0].State != "running" {
		t.Fatalf("procs = %+v", pr.Procs)
	}

	a.reportAlarm(agentproto.Alarm{Kind: agentproto.AlarmKindHighLoad, Severity: "warn", Message: "hot"})
	env = rec.last(t)
	if env.Type != agentproto.MsgAlarm {
		t.Fatalf("type = %q; want alarm", env.Type)
	}
	var al agentproto.Alarm
	mustDecode(t, env, &al)
	if al.Kind != agentproto.AlarmKindHighLoad || al.Message != "hot" {
		t.Fatalf("alarm = %+v", al)
	}
}

// TestOnConnectedHelloAck 覆盖握手成功路径：发 hello（带令牌/标识）→
// 收 ack 校准心跳周期、面板权威元数据覆盖本地 → 起心跳、回调 OnHelloOK。
func TestOnConnectedHelloAck(t *testing.T) {
	a := newDispatchApp()
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	a.version = "v1.2.3-batch"
	a.cfg.Token = "tok-1"
	a.cfg.AgentID = "ag-1"
	helloOK := false
	a.OnHelloOK = func() { helloOK = true }
	rec := &recorder{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- a.OnConnected(ctx, rec.send) }()

	deadline := time.Now().Add(2 * time.Second)
	for rec.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("hello never sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	env := rec.last(t)
	if env.Type != agentproto.MsgHello {
		t.Fatalf("type = %q; want hello", env.Type)
	}
	var h agentproto.Hello
	mustDecode(t, env, &h)
	if h.Token != "tok-1" || h.AgentID != "ag-1" || h.Version != "v1.2.3-batch" {
		t.Fatalf("hello = %+v", h)
	}

	a.mu.Lock()
	ch := a.hello
	a.mu.Unlock()
	if ch == nil {
		t.Fatal("hello ack chan not armed")
	}
	ack, _ := agentproto.NewEnvelope("", agentproto.MsgHelloAck, agentproto.HelloAck{
		HeartbeatIntervalSec: 45,
		Meta:                 agentproto.NodeMeta{Role: "entry", ISP: "hw"},
	})
	ch <- ack

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("OnConnected = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnConnected did not return after ack")
	}
	if got := a.currentInterval(); got != 45*time.Second {
		t.Fatalf("interval = %v; want 45s", got)
	}
	if a.cfg.Meta.Role != "entry" || a.cfg.Meta.ISP != "hw" {
		t.Fatalf("meta not overridden: %+v", a.cfg.Meta)
	}
	if !helloOK {
		t.Fatal("OnHelloOK not called")
	}
	a.mu.Lock()
	started := a.hbStop != nil
	a.mu.Unlock()
	if !started {
		t.Fatal("heartbeat not started")
	}
	a.stopHeartbeat()
}

// TestOnConnectedCancelWhileWaiting 覆盖握手等待期取消：ctx 取消即返回错误。
func TestOnConnectedCancelWhileWaiting(t *testing.T) {
	a := newDispatchApp()
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.OnConnected(ctx, func(agentproto.Envelope) error { return nil })
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		a.mu.Lock()
		h := a.hello
		a.mu.Unlock()
		if h != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hello chan never armed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("cancelled wait should error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no return after cancel")
	}
}

// TestCollectCerts 覆盖配置证书挖掘：cert 键命中 inline PEM 出状态，
// 空 ConfigPath 跳过。
func TestCollectCerts(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Now().Add(90 * 24 * time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hk.example.com"},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "xray.json")
	body := `{"tls":{"certificate":` + strconv.Quote(pemText) + `}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newDispatchApp()
	a.cfg.Procs = []config.ProcSpec{{Name: "xray", ConfigPath: cfgPath}, {Name: "bare"}}
	got := a.collectCerts()
	if len(got) != 1 {
		t.Fatalf("certs = %d, want 1", len(got))
	}
	if got[0].Domain != "hk.example.com" || got[0].NotAfter.Unix() != notAfter.Unix() {
		t.Fatalf("status = %+v; want hk.example.com @ %v", got[0], notAfter)
	}
}

// TestBuildHeartbeatAndStartHeartbeat 覆盖心跳组装与周期发送：
// 快照带进程状态与时间戳；周期发包按 seq 递增；stop 后停发。
func TestBuildHeartbeatAndStartHeartbeat(t *testing.T) {
	a := newDispatchApp()
	a.version = "v1.2.3-batch"
	a.Procs = func() []agentproto.ProcStatus {
		return []agentproto.ProcStatus{{Name: "xray", State: "running"}}
	}
	hb := a.buildHeartbeat()
	if len(hb.Procs) != 1 || hb.Procs[0].Name != "xray" {
		t.Fatalf("hb procs = %+v", hb.Procs)
	}
	if hb.At.IsZero() {
		t.Fatal("hb timestamp not set")
	}

	a.setInterval(20 * time.Millisecond)
	rec := &recorder{}
	a.setSend(rec.send)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.startHeartbeat(ctx, rec.send)

	deadline := time.Now().Add(2 * time.Second)
	for rec.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("heartbeats = %d; want >= 2", rec.count())
		}
		time.Sleep(5 * time.Millisecond)
	}
	env := rec.last(t)
	if env.Type != agentproto.MsgHeartbeat {
		t.Fatalf("type = %q; want heartbeat", env.Type)
	}
	var out agentproto.Heartbeat
	mustDecode(t, env, &out)
	if len(out.Procs) != 1 || out.Procs[0].Name != "xray" {
		t.Fatalf("heartbeat procs = %+v", out.Procs)
	}

	a.stopHeartbeat()
	n := rec.count()
	time.Sleep(100 * time.Millisecond)
	if rec.count() != n {
		t.Fatalf("heartbeat continued after stop: %d -> %d", n, rec.count())
	}
}

// TestProcSpecLookup 进程规格查找：命中与未命中两条口径。
func TestProcSpecLookup(t *testing.T) {
	a := newDispatchApp()
	a.cfg.Procs = []config.ProcSpec{{Name: "xray"}, {Name: "relay"}}
	if _, ok := a.procSpec("xray"); !ok {
		t.Fatal("xray should be found")
	}
	if _, ok := a.procSpec("relay"); !ok {
		t.Fatal("relay should be found")
	}
	if _, ok := a.procSpec("ghost"); ok {
		t.Fatal("ghost should not be found")
	}
}

// TestOnMessageConfigPushBadPayload 载荷解码失败：无从定位 proc/version，
// ack 无从构造，静默放弃不外发。
func TestOnMessageConfigPushBadPayload(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	env := agentproto.Envelope{
		V: agentproto.ProtocolVersion, ID: "bad-1", Type: agentproto.MsgConfigPush,
		Payload: json.RawMessage(`{not json`),
	}
	a.OnMessage(context.Background(), env, a.sendIfConnected)
	if rec.count() != 0 {
		t.Fatalf("bad payload should not reply; got %d", rec.count())
	}
}

// TestOnMessageConfigPushSendFails 应答发出失败：走失败分支只记日志，
// ack 仍构造并发一次。
func TestOnMessageConfigPushSendFails(t *testing.T) {
	a := newDispatchApp()
	delivered := make(chan agentproto.Envelope, 1)
	a.setSend(func(env agentproto.Envelope) error {
		delivered <- env
		return errors.New("link down")
	})

	env, err := agentproto.NewEnvelope("p9", agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: "ghost", Kind: "xray", Version: "v9", Payload: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	select {
	case got := <-delivered:
		var ack agentproto.ConfigAck
		mustDecode(t, got, &ack)
		if ack.OK || ack.Proc != "ghost" {
			t.Fatalf("ack = %+v; want rejected ghost", ack)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ack not attempted")
	}
}

// TestMaybeCalibrateReports 有套餐容量且面板可达：测速结果回报一次，
// 二次调用走 calibrated 短路。
func TestMaybeCalibrateReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/speedtest/bytes" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), 4<<20))
	}))
	defer srv.Close()

	a := newDispatchApp()
	a.cfg.PanelURL = srv.URL
	a.cfg.Meta.BwDownMbps = 200
	rec := &recorder{}
	a.maybeCalibrate(rec.send)
	a.maybeCalibrate(rec.send)

	if got := rec.count(); got != 1 {
		t.Fatalf("calibrations = %d; want 1", got)
	}
	last := rec.last(t)
	if last.Type != agentproto.MsgCalibrate {
		t.Fatalf("type = %q", last.Type)
	}
	var cal agentproto.Calibrate
	mustDecode(t, last, &cal)
	if cal.MeasuredDownMbps <= 0 || cal.Bytes <= 0 {
		t.Fatalf("cal = %+v; want positive measurement", cal)
	}
}

// TestMaybeCalibrateFailures 面板地址非法与面板不可达：只记日志不上报。
func TestMaybeCalibrateFailures(t *testing.T) {
	for name, url := range map[string]string{
		"bad scheme":  "",
		"unreachable": "http://127.0.0.1:9",
	} {
		t.Run(name, func(t *testing.T) {
			a := newDispatchApp()
			a.cfg.PanelURL = url
			a.cfg.Meta.BwDownMbps = 200
			rec := &recorder{}
			a.maybeCalibrate(rec.send)
			if rec.count() != 0 {
				t.Fatalf("%s: calibrations = %d; want 0", name, rec.count())
			}
		})
	}
}
