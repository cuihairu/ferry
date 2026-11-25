package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/configd"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/agent/internal/traffic"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// badPayload builds an envelope whose payload cannot be decoded (RawMessage with invalid JSON).
func badPayload(id, typ string) agentproto.Envelope {
	return agentproto.Envelope{
		V: agentproto.ProtocolVersion, ID: id, Type: typ,
		Payload: json.RawMessage(`{not json`),
	}
}

// TestOnConnectedSendFails covers hello failing to send: the handshake returns an error right away.
func TestOnConnectedSendFails(t *testing.T) {
	a := newDispatchApp()
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	err := a.OnConnected(context.Background(), func(agentproto.Envelope) error {
		return errors.New("link down")
	})
	if err == nil || err.Error() != "link down" {
		t.Fatalf("OnConnected err = %v; want link down", err)
	}
}

// TestOnConnectedHelloAckTimeout covers a handshake reply timeout (helloTimeout=10s): it returns a
// timeout error and does not start the heartbeat.
func TestOnConnectedHelloAckTimeout(t *testing.T) {
	a := newDispatchApp()
	a.traf = traffic.New(nil, time.Second, nil, a.log)
	start := time.Now()
	err := a.OnConnected(context.Background(), func(agentproto.Envelope) error { return nil })
	if err == nil || err.Error() != "hello ack timeout" {
		t.Fatalf("err = %v; want hello ack timeout", err)
	}
	if el := time.Since(start); el < helloTimeout {
		t.Fatalf("returned after %v; want >= %v", el, helloTimeout)
	}
	a.mu.Lock()
	started := a.hbStop != nil
	a.mu.Unlock()
	if started {
		t.Fatal("heartbeat must not start on handshake timeout")
	}
}

// TestOnMessageHelloAckDropsWhenFull covers a full reply channel (capacity 1, already occupied): the
// second frame is dropped by the non-blocking send via default and never blocks the read loop.
func TestOnMessageHelloAckDropsWhenFull(t *testing.T) {
	a := newDispatchApp()
	a.hello = make(chan agentproto.Envelope, 1)
	first, _ := agentproto.NewEnvelope("h0", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.hello <- first // fill it up
	second, _ := agentproto.NewEnvelope("h1", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.OnMessage(context.Background(), second, a.sendIfConnected) // should return immediately without blocking
	select {
	case got := <-a.hello:
		if got.ID != "h0" {
			t.Fatalf("kept id = %q; want h0 (older frame preserved)", got.ID)
		}
	default:
		t.Fatal("original frame lost")
	}
}

// TestOnMessageProcCtlBadPayload covers a proc.control payload that fails to decode: a rejected ack is returned.
func TestOnMessageProcCtlBadPayload(t *testing.T) {
	a := newDispatchApp() // safe even with a nil mgr: decoding fails before Control runs
	rec := &recorder{}
	a.setSend(rec.send)

	a.OnMessage(context.Background(), badPayload("c9", agentproto.MsgProcCtl), a.sendIfConnected)
	if rec.count() != 1 {
		t.Fatalf("replies = %d; want 1", rec.count())
	}
	var ack agentproto.ProcCtlAck
	mustDecode(t, rec.last(t), &ack)
	if ack.OK || ack.Error == "" {
		t.Fatalf("ack = %+v; want decode error", ack)
	}
}

// TestOnMessageProcLogsBadPayload covers a proc.logs payload that fails to decode: an error ack is returned.
func TestOnMessageProcLogsBadPayload(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	a.OnMessage(context.Background(), badPayload("l9", agentproto.MsgProcLogs), a.sendIfConnected)
	if rec.count() != 1 {
		t.Fatalf("replies = %d; want 1", rec.count())
	}
	var ack agentproto.ProcLogsAck
	mustDecode(t, rec.last(t), &ack)
	if ack.Error == "" {
		t.Fatalf("ack = %+v; want decode error", ack)
	}
}

// TestOnMessageCalibrateAck covers a speed-test confirmation frame: log only, no outbound reply.
func TestOnMessageCalibrateAck(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	env, err := agentproto.NewEnvelope("cal-ack-1", agentproto.MsgCalibrateAck,
		agentproto.CalibrateAck{Accepted: true, EffectiveDownMbps: 180, Note: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	a.OnMessage(context.Background(), env, a.sendIfConnected)
	if rec.count() != 0 {
		t.Fatalf("calibrate ack must not reply; got %d", rec.count())
	}
}

// startManagedApp starts an App with a real child process (sh -c sleep 60): the mgr is already Started,
// wait for the process to be running; cleanup stops it first and then cancels supervision.
func startManagedApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a := newDispatchApp()
	a.cfg.Procs = []config.ProcSpec{{
		Name: "xray", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
		ConfigPath: filepath.Join(dir, "xray.json"),
	}}
	a.mgr = procs.New(a.cfg.Procs, a.log)
	a.cfgd = configd.New(a.log)
	ctx, cancel := context.WithCancel(context.Background())
	a.mgr.Start(ctx)
	t.Cleanup(func() {
		_ = a.mgr.Control("xray", agentproto.ProcActionStop)
		cancel()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := a.mgr.Statuses()
		if len(st) == 1 && st[0].State == agentproto.ProcRunning {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("proc not running: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitReply waits until rec has received the want-th message (counted from before), then returns the last frame.
func waitReply(t *testing.T, rec *recorder, before, want int) agentproto.Envelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for rec.count() < before+want {
		if time.Now().After(deadline) {
			t.Fatalf("no reply (have %d, want %d)", rec.count(), before+want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return rec.last(t)
}

// TestOnMessageProcCtlAgainstManagedProc covers control commands aimed at a managed process:
// both stop and start succeed and return an OK ack (paired with the unknown-proc rejection branch).
func TestOnMessageProcCtlAgainstManagedProc(t *testing.T) {
	a := startManagedApp(t)
	rec := &recorder{}
	a.setSend(rec.send)

	sendCtl := func(id, action string) agentproto.ProcCtlAck {
		t.Helper()
		env, err := agentproto.NewEnvelope(id, agentproto.MsgProcCtl,
			agentproto.ProcCtl{Proc: "xray", Action: action})
		if err != nil {
			t.Fatal(err)
		}
		before := rec.count()
		a.OnMessage(context.Background(), env, a.sendIfConnected)
		var ack agentproto.ProcCtlAck
		mustDecode(t, waitReply(t, rec, before, 1), &ack)
		return ack
	}

	if ack := sendCtl("c2", agentproto.ProcActionStop); !ack.OK || ack.Error != "" {
		t.Fatalf("stop ack = %+v; want ok", ack)
	}
	if ack := sendCtl("c3", agentproto.ProcActionStart); !ack.OK || ack.Error != "" {
		t.Fatalf("start ack = %+v; want ok", ack)
	}
}

// TestOnMessageProcLogsEmptyRing covers the limit clamping paths of log fetching (0/negative filled with the
// default 200, capped at 1000, range passed through): an empty ring buffer returns an empty line set without error.
func TestOnMessageProcLogsEmptyRing(t *testing.T) {
	a := startManagedApp(t)
	rec := &recorder{}
	a.setSend(rec.send)

	for i, limit := range []int{0, -3, 5000, 50} {
		env, err := agentproto.NewEnvelope("l1", agentproto.MsgProcLogs,
			agentproto.ProcLogsReq{Proc: "xray", Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		before := rec.count()
		a.OnMessage(context.Background(), env, a.sendIfConnected)
		var ack agentproto.ProcLogsAck
		mustDecode(t, waitReply(t, rec, before, 1), &ack)
		if ack.Error != "" {
			t.Fatalf("case %d (limit %d): ack error = %q; want none", i, limit, ack.Error)
		}
		if ack.Proc != "xray" || len(ack.Lines) != 0 {
			t.Fatalf("case %d: ack = %+v; want empty lines for xray", i, ack)
		}
	}
}

// TestOnMessageConfigPushApplies covers a config push succeeding end to end: sha256 verification, writing to disk,
// reload (of managed processes), then an OK ack, with the on-disk content matching the push.
func TestOnMessageConfigPushApplies(t *testing.T) {
	a := startManagedApp(t)
	rec := &recorder{}
	a.setSend(rec.send)

	payload := `{"tls":{"rejectUnknownSni":false}}`
	sum := sha256.Sum256([]byte(payload))
	env, err := agentproto.NewEnvelope("p2", agentproto.MsgConfigPush, agentproto.ConfigPush{
		Proc: "xray", Kind: "xray", Version: "v2", Payload: payload, Sha256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	var ack agentproto.ConfigAck
	mustDecode(t, waitReply(t, rec, 0, 1), &ack)
	if !ack.OK || ack.Error != "" || ack.Reverted {
		t.Fatalf("ack = %+v; want applied", ack)
	}
	if ack.Proc != "xray" || ack.Version != "v2" {
		t.Fatalf("ack proc/version = %q/%q; want xray/v2", ack.Proc, ack.Version)
	}
	got, err := os.ReadFile(a.cfg.Procs[0].ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("config on disk = %q; want %q", got, payload)
	}
}

// failRecorder counts attempts and always fails (used by the heartbeat/speed-test send-failure branches).
type failRecorder struct {
	mu      sync.Mutex
	attempt int
}

func (r *failRecorder) send(agentproto.Envelope) error {
	r.mu.Lock()
	r.attempt++
	r.mu.Unlock()
	return errors.New("link down")
}

func (r *failRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.attempt
}

// TestStartHeartbeatSendFails covers a heartbeat that fails to send: log it and exit the heartbeat loop,
// without retrying.
func TestStartHeartbeatSendFails(t *testing.T) {
	a := newDispatchApp()
	a.setInterval(10 * time.Millisecond)
	rec := &failRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.startHeartbeat(ctx, rec.send)

	deadline := time.Now().Add(2 * time.Second)
	for rec.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never attempted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	n := rec.count()
	time.Sleep(150 * time.Millisecond)
	if got := rec.count(); got != n {
		t.Fatalf("attempts kept growing after send failure: %d -> %d", n, got)
	}
}

// TestStartHeartbeatCtxCancel covers the heartbeat exiting on ctx cancellation: the counter settles and stops growing.
func TestStartHeartbeatCtxCancel(t *testing.T) {
	a := newDispatchApp()
	a.setInterval(10 * time.Millisecond)
	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	a.startHeartbeat(ctx, rec.send)

	deadline := time.Now().Add(2 * time.Second)
	for rec.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never sent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	// one in-flight tick is still allowed to land after cancellation (select may already have picked a timer.C
	// that had already fired; observed 1 -> 2 on slow CI machines), so assert "the counter eventually settles":
	// two consecutive equal frames mean the loop has exited; if it had not, the counter would keep growing at the
	// 10ms tick rate and the stabilize window would never be satisfied.
	stabilize := time.Now().Add(2 * time.Second)
	prev := rec.count()
	for {
		time.Sleep(150 * time.Millisecond)
		cur := rec.count()
		if cur == prev {
			break
		}
		if time.Now().After(stabilize) {
			t.Fatalf("heartbeats kept growing after ctx cancel: %d -> %d", prev, cur)
		}
		prev = cur
	}
}

// TestWatchAlarmsExpiredCert covers an already-expired certificate: the alarm message uses the "already expired as of" wording.
func TestWatchAlarmsExpiredCert(t *testing.T) {
	a := newDispatchApp()
	rec := &recorder{}
	a.setSend(rec.send)

	hb := agentproto.Heartbeat{
		Certs: []agentproto.CertStatus{{Domain: "old.com", NotAfter: time.Now().Add(-24 * time.Hour)}},
	}
	a.watchAlarms(hb)
	if got := rec.count(); got != 1 {
		t.Fatalf("alarms = %d; want 1", got)
	}
	var al agentproto.Alarm
	mustDecode(t, rec.last(t), &al)
	if !strings.Contains(al.Message, "已于") || !strings.Contains(al.Message, "过期") {
		t.Fatalf("message = %q; want expired wording", al.Message)
	}
}

// TestMaybeCalibrateSendFails covers the speed test finishing but its report failing: log only, no retry, no resend.
func TestMaybeCalibrateSendFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1<<20))
	}))
	defer srv.Close()

	a := newDispatchApp()
	a.cfg.PanelURL = srv.URL
	a.cfg.Meta.BwDownMbps = 100
	rec := &failRecorder{}
	a.maybeCalibrate(rec.send)
	if rec.count() != 1 {
		t.Fatalf("send attempts = %d; want 1", rec.count())
	}
	// a second call short-circuits on calibrated and does not retry
	a.maybeCalibrate(rec.send)
	if rec.count() != 1 {
		t.Fatalf("attempts after recalibrate = %d; want 1", rec.count())
	}
}

// TestRunConnectsAndHandshakes covers the whole Run path (a real link dial): the local ws panel receives
// hello and answers hello_ack, Run completes the handshake through OnConnected and fires OnHelloOK; after ctx
// cancellation Run returns context.Canceled. Both config branches, built-in probing and spool, run one round each.
func TestRunConnectsAndHandshakes(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(raw, &env); err != nil || env.Type != agentproto.MsgHello {
			return
		}
		var h agentproto.Hello
		if err := env.Decode(&h); err != nil || h.Token != "tok-run" {
			return
		}
		ack, err := agentproto.NewEnvelope("ack-run", agentproto.MsgHelloAck, agentproto.HelloAck{})
		if err != nil {
			return
		}
		raw, err = json.Marshal(ack)
		if err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
			return
		}
		for { // handshake complete, keep receiving heartbeats/reports until the peer disconnects
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	for _, mode := range []string{"probe", "spool"} {
		t.Run(mode, func(t *testing.T) {
			cfg := config.Config{
				PanelURL: "ws://" + strings.TrimPrefix(srv.URL, "http://"),
				Token:    "tok-run",
			}
			if mode == "spool" {
				cfg.SpoolDir = t.TempDir()
			}
			a := New(cfg, "v1.2.3-test")
			helloOK := make(chan struct{}, 1)
			a.OnHelloOK = func() { helloOK <- struct{}{} }

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runErr := make(chan error, 1)
			go func() { runErr <- a.Run(ctx) }()

			select {
			case <-helloOK:
			case <-time.After(5 * time.Second):
				t.Fatal("handshake did not complete (OnHelloOK never called)")
			}
			cancel()
			select {
			case err := <-runErr:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Run err = %v; want context.Canceled", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return after cancel")
			}
		})
	}
}
