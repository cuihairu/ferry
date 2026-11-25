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

	// idempotent: already consumed data is not resent
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
	// simulating a concurrent-append tear: a complete line plus a partial line without a newline
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
	// complete the partial line; it is consumed next round
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
	// the offset has not advanced: after recovery the resend succeeds
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

// TestWatchForwardsUntilCancel covers Watch's periodic scan-and-forward: what is written lands after a scan,
// and once the ctx is cancelled Watch returns ctx.Err(); a missing directory does not crash it (IsNotExist counts as empty).
func TestWatchForwardsUntilCancel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "later") // the directory does not exist yet when Watch starts
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
	// the directory still does not exist: Watch must not exit because of it.
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

// TestWriteRejectsBadArgs covers missing arguments being refused outright, never silently swallowed.
func TestWriteRejectsBadArgs(t *testing.T) {
	env := agentproto.Envelope{V: agentproto.ProtocolVersion, ID: "e1", Type: "probe.report"}
	if err := Write("", "probe", env); err == nil {
		t.Error("empty dir should error")
	}
	if err := Write(t.TempDir(), "", env); err == nil {
		t.Error("empty role should error")
	}
}

// TestWriteDirNotDirectory covers the spool path being blocked by a plain file: the mkdir failure surfaces as an error.
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

// TestWriteBadPayload covers a payload of invalid JSON bytes: refused at serialization, never written to a file.
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

// TestDrainMissingDirIsNoop covers a directory not yet created being the normal state (the role has not started), not an error.
func TestDrainMissingDirIsNoop(t *testing.T) {
	w := &watcher{dir: filepath.Join(t.TempDir(), "absent"), offsets: map[string]int64{}}
	if err := w.drain(func(agentproto.Envelope) error { return nil }); err != nil {
		t.Fatalf("drain on missing dir = %v; want nil", err)
	}
}

// TestDrainDirReadError covers the directory path being blocked by a file: ReadDir errors and the error propagates up.
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

// TestDrainFileOpenRace covers a file deleted after ReadDir: IsNotExist is tolerated without interrupting the whole round.
func TestDrainFileOpenRace(t *testing.T) {
	w := &watcher{dir: t.TempDir(), offsets: map[string]int64{}}
	if err := w.drainFile(filepath.Join(w.dir, "gone.out"),
		func(agentproto.Envelope) error { return nil }); err != nil {
		t.Fatalf("drainFile on missing file = %v; want nil", err)
	}
}

// TestDrainSkipsNonSpoolFiles covers subdirectories and non-.out files in the directory taking no part in consumption.
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
