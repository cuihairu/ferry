package spool

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
