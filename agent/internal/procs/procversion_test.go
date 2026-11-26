package procs

import (
	"io"
	"log"
	"testing"

	"github.com/cuihairu/ferry/agent/internal/config"
)

// TestProcStatusVersion covers F-0816: the agent's build version is attached
// to every managed-process snapshot (relay/probe ship in the same binary), and
// the legacy constructor leaves the field empty for older callers.
func TestProcStatusVersion(t *testing.T) {
	m := NewWithVersion([]config.ProcSpec{{Name: "relay", Exec: "sh"}},
		log.New(io.Discard, "", 0), "v9.8.7")
	st := m.Statuses()
	if len(st) != 1 || st[0].Version != "v9.8.7" {
		t.Fatalf("status with version = %+v", st)
	}
	legacy := New([]config.ProcSpec{{Name: "relay", Exec: "sh"}}, log.New(io.Discard, "", 0))
	if got := legacy.Statuses()[0].Version; got != "" {
		t.Fatalf("legacy version = %q; want empty", got)
	}
}
