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

// badPayload 造载荷无法解码的信封（RawMessage 非法 JSON）。
func badPayload(id, typ string) agentproto.Envelope {
	return agentproto.Envelope{
		V: agentproto.ProtocolVersion, ID: id, Type: typ,
		Payload: json.RawMessage(`{not json`),
	}
}

// TestOnConnectedSendFails hello 发出即失败：握手直接报错返回。
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

// TestOnConnectedHelloAckTimeout 握手应答超时（helloTimeout=10s）：返回超时
// 错误且不启心跳。
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

// TestOnMessageHelloAckDropsWhenFull 应答通道已满（容量 1 且占位）：第二帧
// 非阻塞投递走 default 丢弃，不阻塞读循环。
func TestOnMessageHelloAckDropsWhenFull(t *testing.T) {
	a := newDispatchApp()
	a.hello = make(chan agentproto.Envelope, 1)
	first, _ := agentproto.NewEnvelope("h0", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.hello <- first // 占满
	second, _ := agentproto.NewEnvelope("h1", agentproto.MsgHelloAck, agentproto.HelloAck{})
	a.OnMessage(context.Background(), second, a.sendIfConnected) // 应立即返回不阻塞
	select {
	case got := <-a.hello:
		if got.ID != "h0" {
			t.Fatalf("kept id = %q; want h0 (older frame preserved)", got.ID)
		}
	default:
		t.Fatal("original frame lost")
	}
}

// TestOnMessageProcCtlBadPayload proc.control 载荷解码失败：回拒收 ack。
func TestOnMessageProcCtlBadPayload(t *testing.T) {
	a := newDispatchApp() // mgr 为 nil 也安全：解码失败在 Control 之前
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

// TestOnMessageProcLogsBadPayload proc.logs 载荷解码失败：回错误 ack。
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

// TestOnMessageCalibrateAck 测速确认帧：仅记日志，无外发应答。
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

// startManagedApp 起带真实子进程（sh -c sleep 60）的 App：mgr 已 Start，
// 等进程 running；清理先 stop 再取消监管。
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

// waitReply 等待 rec 收到第 want 条消息（从 before 起算）后返回最后一帧。
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

// TestOnMessageProcCtlAgainstManagedProc 对在管进程的 control 指令：
// stop 与 start 都成功并回 OK ack（配对 unknown proc 的拒收分支）。
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

// TestOnMessageProcLogsEmptyRing 日志拉取的 limit 收口路径（0/负数补默认
// 200、上限封顶 1000、区间原样）：空环形缓冲回空行集不报错。
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

// TestOnMessageConfigPushApplies 配置下发全链路成功：sha256 校验、落盘、
// reload（在管进程）后回 OK ack，磁盘内容与推送一致。
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

// failRecorder 记录尝试次数并固定失败（心跳/测速发送失败分支用）。
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

// TestStartHeartbeatSendFails 心跳发出失败：记日志并退出心跳循环，
// 不再继续尝试。
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

// TestStartHeartbeatCtxCancel 心跳随 ctx 取消退出：计数稳定不再增长。
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
	n := rec.count()
	time.Sleep(150 * time.Millisecond)
	if got := rec.count(); got != n {
		t.Fatalf("heartbeats continued after ctx cancel: %d -> %d", n, got)
	}
}

// TestWatchAlarmsExpiredCert 已过期证书：告警消息用「已于…过期」措辞。
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

// TestMaybeCalibrateSendFails 测速完成但回传失败：只记日志，不重试不外发。
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
	// 二次调用走 calibrated 短路，不再尝试
	a.maybeCalibrate(rec.send)
	if rec.count() != 1 {
		t.Fatalf("attempts after recalibrate = %d; want 1", rec.count())
	}
}

// TestRunConnectsAndHandshakes Run 全链路（真 link 拨号）：本地 ws 面板收
// hello、回 hello_ack，Run 经 OnConnected 握手成功并触发 OnHelloOK；ctx
// 取消后 Run 返回 context.Canceled。探测内建与 spool 两个配置分支各跑一轮。
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
		for { // 握手完成，继续收心跳/上报直至对端断开
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
