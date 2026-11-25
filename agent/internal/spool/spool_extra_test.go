package spool

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestWriteOpenFailsWithSlashRole covers a role containing a path separator: the parent directory of the assembled target path
// does not exist, so the open failure must error (instead of silently succeeding).
func TestWriteOpenFailsWithSlashRole(t *testing.T) {
	dir := t.TempDir()
	err := Write(dir, "sub/role", mustEnv(t, "1", agentproto.MsgProbeReport))
	if err == nil || !strings.Contains(err.Error(), "spool: open") {
		t.Fatalf("err = %v; want spool: open", err)
	}
}

// TestDrainRotationResetsOffset covers a file being rotated/recreated (smaller): the offset resets and consumption restarts,
// so new content is not dropped because an old offset skipped past it.
func TestDrainRotationResetsOffset(t *testing.T) {
	dir := t.TempDir()
	path := pathFor(dir, "probe")

	var got []string
	forward := func(e agentproto.Envelope) error {
		got = append(got, e.ID)
		return nil
	}
	w := &watcher{dir: dir, offsets: map[string]int64{}}

	if err := Write(dir, "probe", mustEnv(t, "long-content-1", agentproto.MsgProbeReport)); err != nil {
		t.Fatal(err)
	}
	if err := w.drain(forward); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "long-content-1" {
		t.Fatalf("first drain = %v", got)
	}

	// rotate: rewrite the whole file with shorter new content
	fresh := mustEnv(t, "s", agentproto.MsgProbeReport)
	raw, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := w.drain(forward); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "s" {
		t.Fatalf("after rotation = %v; want fresh content", got)
	}
}

// TestDrainSkipsEmptyLines covers empty lines (consecutive ones included) being skipped without a bogus send.
func TestDrainSkipsEmptyLines(t *testing.T) {
	dir := t.TempDir()
	env := mustEnv(t, "e1", agentproto.MsgProbeReport)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw) + "\n\n\n" + string(raw) + "\n"
	if err := os.WriteFile(pathFor(dir, "probe"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var got []string
	w := &watcher{dir: dir, offsets: map[string]int64{}}
	if err := w.drain(func(e agentproto.Envelope) error {
		got = append(got, e.ID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("forwarded = %v; want exactly 2 (empty lines skipped)", got)
	}
}
