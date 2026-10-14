package app

import (
	"context"
	"io"
	"log"
	"sync"
	"testing"
	"time"

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
func newDispatchApp() *App {
	return &App{
		log:        log.New(io.Discard, "", 0),
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
