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
	"strings"
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

	// no report below the threshold
	if w.observe(10, base.Add(1*time.Minute)) {
		t.Fatal("low cpu should not fire")
	}
	// the first reading over the threshold only accumulates; the second one reports (last is the zero value then, so it takes effect immediately)
	if w.observe(95, base.Add(2*time.Minute)) {
		t.Fatal("single spike should not fire")
	}
	if !w.observe(95, base.Add(3*time.Minute)) {
		t.Fatal("sustained load should fire")
	}
	// sustained high load during the cooldown does not re-report (streak keeps accumulating)
	if w.observe(95, base.Add(4*time.Minute)) || w.observe(95, base.Add(10*time.Minute)) {
		t.Fatal("cooldown should suppress")
	}
	// reports again after the cooldown (30m): last was +3m, +34m is past it
	if !w.observe(95, base.Add(34*time.Minute)) {
		t.Fatal("after cooldown should fire again")
	}
	// dropping back resets it: rising again needs two consecutive readings, and the new cooldown has not passed so no report
	if w.observe(10, base.Add(35*time.Minute)) {
		t.Fatal("recovered cpu should reset streak")
	}
	if w.observe(99, base.Add(36*time.Minute)) {
		t.Fatal("fresh streak needs two samples")
	}
	if w.observe(99, base.Add(37*time.Minute)) {
		t.Fatal("fresh streak still in cooldown")
	}
	// last reported at +34m, +65m crosses the cooldown and can report again
	if !w.observe(99, base.Add(65*time.Minute)) {
		t.Fatal("third fire after cooldown")
	}
}

// newDispatchApp builds a minimal dispatchable App: no real process/probe/traffic components are attached.
// version matches the argument of the production constructor New(cfg, version); the handshake assertion needs it.
func newDispatchApp() *App {
	return &App{
		log:        log.New(io.Discard, "", 0),
		version:    "v1.2.3-batch",
		interval:   30 * time.Second,
		certAlarms: map[string]time.Time{},
	}
}

// recorder captures the replies sent by sendIfConnected.
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
	a := newDispatchApp() // hello channel is nil (before connect/handshake)
	env, _ := agentproto.NewEnvelope("h1", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.OnMessage(context.Background(), env, a.sendIfConnected) // must not panic
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
	// a zero interval leaves the current period unchanged
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

	// payload fails to decode
	bad, _ := agentproto.NewEnvelope("u1", agentproto.MsgUpgrade, "junk")
	a.OnMessage(context.Background(), bad, a.sendIfConnected)
	// missing version/url
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

	// acceptance is acked immediately; the background doUpgrade fails on an unreachable URL and only logs (binary unchanged)
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
	a := newDispatchApp() // empty cfg.Procs means every proc is unknown
	replies := make(chan agentproto.Envelope, 4)
	a.setSend(func(env agentproto.Envelope) error {
		replies <- env
		return nil
	})

	env, _ := agentproto.NewEnvelope("p1", agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: "ghost", Kind: "xray", Version: "v1", Payload: "{}"})
	a.OnMessage(context.Background(), env, a.sendIfConnected) // runs asynchronously

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
	// the production constructor always initializes traf (app.go:89); supply a real instance here so it can be cleared
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	a.OnDisconnected()
	if err := a.sendIfConnected(agentproto.Envelope{}); err == nil {
		t.Fatal("send after disconnect should fail")
	}
}

func TestMaybeCalibrateSkipsWithoutPlan(t *testing.T) {
	a := newDispatchApp() // Meta.BwDownMbps=0 means no speed test
	rec := &recorder{}
	a.maybeCalibrate(rec.send)
	a.maybeCalibrate(rec.send) // a second call short-circuits on calibrated
	if rec.count() != 0 {
		t.Fatalf("calibrate without plan should not send; got %d", rec.count())
	}
}

func TestWatchAlarmsCertExpiryThrottle(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	now := time.Now()
	notAfter := now.Add(10 * 24 * time.Hour) // inside the 30-day window
	hb := agentproto.Heartbeat{Certs: []agentproto.CertStatus{{Domain: "a.com", NotAfter: notAfter}}, At: now}

	a.watchAlarms(hb)
	a.watchAlarms(hb) // the same certificate with the same validity is reported once
	if got := rec.count(); got != 1 {
		t.Fatalf("alarms = %d; want 1 (throttled)", got)
	}
	var al agentproto.Alarm
	mustDecode(t, rec.last(t), &al)
	if al.Kind != agentproto.AlarmKindCertExpiry {
		t.Fatalf("kind = %q; want cert expiry", al.Kind)
	}

	// after renewal (a NotAfter change) it is eligible to alarm again
	hb.Certs[0].NotAfter = notAfter.Add(24 * time.Hour)
	a.watchAlarms(hb)
	if got := rec.count(); got != 2 {
		t.Fatalf("alarms after renew = %d; want 2", got)
	}

	// a far-future certificate does not alarm
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

	a.watchAlarms(high) // streak 1, no report
	if got := rec.count(); got != 0 {
		t.Fatalf("single spike should not alarm; got %d", got)
	}
	a.watchAlarms(high) // streak 2, reports once
	if got := rec.count(); got != 1 {
		t.Fatalf("sustained load should alarm; got %d", got)
	}
	var al agentproto.Alarm
	mustDecode(t, rec.last(t), &al)
	if al.Kind != agentproto.AlarmKindHighLoad {
		t.Fatalf("kind = %q; want high load", al.Kind)
	}

	a.watchAlarms(low) // dropping back clears the streak
	a.watchAlarms(high)
	if got := rec.count(); got != 1 {
		t.Fatalf("fresh streak needs two samples; got %d", got)
	}
	a.watchAlarms(high) // streak 2 but inside the cooldown (30m), suppressed
	if got := rec.count(); got != 1 {
		t.Fatalf("cooldown should suppress; got %d", got)
	}
}

// TestNewWiresComponents covers wiring: even an empty config builds all components (mgr/cfgd/probe/trafs)
// plus the heartbeat snapshot entry point and the alarm throttling map.
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

// TestReportStatusAndAlarm covers process state/alarm reporting: with the sender attached, the proc.report
// and alarm envelopes go out.
func TestReportStatusAndAlarm(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	a.reportStatus(agentproto.ProcStatus{Name: "xray", State: "running"})
	env := rec.last(t)
	if env.Type != agentproto.MsgProcReport {
		t.Fatalf("type = %q; want proc.report", env.Type)
	}
	// FERRY-004: proc_report must carry a non-empty ID (the key for the server-side idempotency window),
	// and a new ID is generated per report (a new ID = a new frame). Semantics in `docs/design/agent架构设计.md` §8.
	if env.ID == "" {
		t.Fatal("proc_report must carry a non-empty envelope id (dedup key)")
	}
	if !strings.HasPrefix(env.ID, "proc-") {
		t.Fatalf("proc_report id = %q; want proc- prefix", env.ID)
	}
	var pr agentproto.ProcReport
	mustDecode(t, env, &pr)
	if len(pr.Procs) != 1 || pr.Procs[0].Name != "xray" || pr.Procs[0].State != "running" {
		t.Fatalf("procs = %+v", pr.Procs)
	}
	a.reportStatus(agentproto.ProcStatus{Name: "xray", State: "running"})
	if env2 := rec.last(t); env2.ID == env.ID {
		t.Fatal("each proc_report must carry a fresh id")
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

// TestOnConnectedHelloAck covers the successful handshake path: send hello (with token/identity) ->
// receive the ack calibrating the heartbeat period, panel-authoritative metadata overriding local values -> start the heartbeat and invoke OnHelloOK.
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

// TestOnConnectedCancelWhileWaiting covers cancellation while waiting for the handshake: a cancelled ctx returns an error.
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

// TestCollectCerts covers certificate mining from configs: a cert key hitting an inline PEM yields state,
// an empty ConfigPath is skipped.
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

// TestBuildHeartbeatAndStartHeartbeat covers heartbeat assembly and periodic sending:
// the snapshot carries process states and a timestamp; periodic sends advance seq; sending stops after stop.
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

// TestProcSpecLookup covers process spec lookup: both the hit and miss paths.
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

// TestOnMessageConfigPushBadPayload covers a payload that fails to decode: proc/version cannot be located,
// no ack can be built, and it is silently dropped without an outbound reply.
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

// TestOnMessageConfigPushSendFails covers a reply that fails to send: the failure branch only logs,
// the ack is still built and sent once.
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

// TestMaybeCalibrateReports covers having plan capacity with the panel reachable: the speed-test result is reported once,
// and a second call short-circuits on calibrated.
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

// TestMaybeCalibrateFailures covers an invalid panel address and an unreachable panel: log only, no report.
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
