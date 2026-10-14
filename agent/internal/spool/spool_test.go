package spool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

func mustEnv(t *testing.T, id, typ string) agentproto.Envelope {
	t.Helper()
	e, err := agentproto.NewEnvelope(id, typ, map[string]string{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWriteAndDrain(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "probe", mustEnv(t, "1", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "probe", mustEnv(t, "2", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	if err := Write("", "probe", mustEnv(t, "x", agentproto.MsgProbeReport)); err == nil {
		t.Fatal("empty dir must fail")
	}

	var got []agentproto.Envelope
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("order/content wrong: %+v", got)
	}

	// 幂等：已消费的不重发
	got = nil
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("redelivered: %+v", got)
	}
}

func TestPartialLineHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.out")
	// 模拟并发追加撕裂：完整行 + 不带换行的半行
	if err := os.WriteFile(path, []byte("{\"v\":1,\"id\":\"a\",\"type\":\"x\"}\n{\"v\":1,\"id\":\"par"), 0644); err != nil {
		t.Fatal(err)
	}
	var got []agentproto.Envelope
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("partial line must wait: %+v", got)
	}
	// 补齐半行，下轮消费
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("tial\"}\n")
	_ = f.Close()
	got = nil
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "partial" {
		t.Fatalf("completed line missing: %+v", got)
	}
}

func TestSendFailureRetries(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "probe", mustEnv(t, "1", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	calls := 0
	send := func(e agentproto.Envelope) error {
		calls++
		return errors.New("offline")
	}
	if err := w.drain(send); err == nil {
		t.Fatal("drain must surface send error")
	}
	// offset 未推进：恢复后重发成功
	var got []agentproto.Envelope
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("retry missing: %+v", got)
	}
}

func TestBadLineSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.out")
	if err := os.WriteFile(path, []byte("not-json\n{\"v\":1,\"id\":\"ok\",\"type\":\"x\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var got []agentproto.Envelope
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Fatalf("bad line must not block: %+v", got)
	}
}

// TestWatchForwardsUntilCancel Watch 周期扫描转发：写盘后被扫描消费，
// ctx 取消后返回 ctx.Err()；目录不存在时不崩（IsNotExist 视同空）。
func TestWatchForwardsUntilCancel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "later") // Watch 起步时目录还不存在
	got := make(chan agentproto.Envelope, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, dir, func(e agentproto.Envelope) error {
			got <- e
			return nil
		})
	}()
	// 目录此时还不存在：Watch 不得因此退出。
	select {
	case err := <-done:
		t.Fatalf("Watch exited on missing dir: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := Write(dir, "probe", mustEnv(t, "w1", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-got:
		if e.ID != "w1" {
			t.Fatalf("forwarded %+v; want w1", e)
		}
	case <-time.After(PollInterval + 3*time.Second):
		t.Fatal("Watch did not forward spooled entry")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Watch err = %v; want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after cancel")
	}
}

// TestWriteRejectsBadArgs 参数缺失直接拒，不静默吞。
func TestWriteRejectsBadArgs(t *testing.T) {
	env := agentproto.Envelope{V: agentproto.ProtocolVersion, ID: "e1", Type: "probe.report"}
	if err := Write("", "probe", env); err == nil {
		t.Error("empty dir should error")
	}
	if err := Write(t.TempDir(), "", env); err == nil {
		t.Error("empty role should error")
	}
}

// TestWriteDirNotDirectory spool 路径被普通文件占住：建目录失败即冒错。
func TestWriteDirNotDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := agentproto.Envelope{V: agentproto.ProtocolVersion, ID: "e1", Type: "probe.report"}
	err := Write(dir, "probe", env)
	if err == nil || !strings.Contains(err.Error(), "spool: mkdir") {
		t.Fatalf("err = %v; want mkdir failure", err)
	}
}

// TestWriteBadPayload 载荷是非法 JSON 字节：序列化阶段就拒，不落文件。
func TestWriteBadPayload(t *testing.T) {
	dir := t.TempDir()
	env := agentproto.Envelope{V: agentproto.ProtocolVersion, Type: "probe.report",
		Payload: json.RawMessage{0xff, 0xfe}}
	err := Write(dir, "probe", env)
	if err == nil || !strings.Contains(err.Error(), "spool: marshal") {
		t.Fatalf("err = %v; want marshal failure", err)
	}
	if _, statErr := os.Stat(pathFor(dir, "probe")); !os.IsNotExist(statErr) {
		t.Fatal("failed write must not create spool file")
	}
}

// TestDrainMissingDirIsNoop 目录还没建出来是常态（角色未起），不算错。
func TestDrainMissingDirIsNoop(t *testing.T) {
	w := &watcher{dir: filepath.Join(t.TempDir(), "absent"), offsets: map[string]int64{}}
	if err := w.drain(func(agentproto.Envelope) error { return nil }); err != nil {
		t.Fatalf("drain on missing dir = %v; want nil", err)
	}
}

// TestDrainDirReadError 目录路径被文件占住：ReadDir 报错并冒上去。
func TestDrainDirReadError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(agentproto.Envelope) error { return nil }); err == nil {
		t.Fatal("drain on non-directory should error")
	}
}

// TestDrainFileOpenRace 文件在 ReadDir 之后被删：容忍 IsNotExist 不打断整轮。
func TestDrainFileOpenRace(t *testing.T) {
	w := &watcher{dir: t.TempDir(), offsets: map[string]int64{}}
	if err := w.drainFile(filepath.Join(w.dir, "gone.out"),
		func(agentproto.Envelope) error { return nil }); err != nil {
		t.Fatalf("drainFile on missing file = %v; want nil", err)
	}
}

// TestDrainSkipsNonSpoolFiles 目录里的子目录与非 .out 文件不参与消费。
func TestDrainSkipsNonSpoolFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub.out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _ := agentproto.NewEnvelope("e1", "probe.report", nil)
	if err := Write(dir, "probe", env); err != nil {
		t.Fatal(err)
	}

	var got []string
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error { got = append(got, e.ID); return nil }); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(got) != 1 || got[0] != "e1" {
		t.Fatalf("forwarded = %v; want only the .out line", got)
	}
}
