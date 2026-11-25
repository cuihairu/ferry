//go:build unix

package procs

import (
	"testing"
)

// TestLogRingSnapshot covers ring wrapping, limit trimming, newline stripping and empty-line skipping.
func TestLogRingSnapshot(t *testing.T) {
	r := newLogRing(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		if n, err := r.Write([]byte(s + "\n")); err != nil || n != len(s)+1 {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	// capacity 3: only the three most recent are kept, in ascending time order
	got := r.snapshot(0)
	if len(got) != 3 || got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Fatalf("snapshot = %v", got)
	}
	// limit takes the most recent n entries
	got = r.snapshot(2)
	if len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Fatalf("limited snapshot = %v", got)
	}
	// empty lines and newline-only lines do not enter the ring
	if _, err := r.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if len(r.snapshot(0)) != 3 {
		t.Fatalf("empty line should not be recorded: %v", r.snapshot(0))
	}
	// a pipe copy can merge several lines into one write: split on embedded newlines
	if _, err := r.Write([]byte("x\ny\n")); err != nil {
		t.Fatal(err)
	}
	got = r.snapshot(2)
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("multiline write snapshot = %v", got)
	}
	// CRLF normalized
	if _, err := r.Write([]byte("win\r\n")); err != nil {
		t.Fatal(err)
	}
	got = r.snapshot(1)
	if len(got) != 1 || got[0] != "win" {
		t.Fatalf("crlf snapshot = %v", got)
	}
}
