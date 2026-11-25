package ringlog

import (
	"strings"
	"testing"
)

// TestRingOverwrite covers ring overwrite plus Entries' ascending-time order and limit truncation.
func TestRingOverwrite(t *testing.T) {
	l := New(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		l.Add(s)
	}
	got := l.Entries(0)
	if len(got) != 3 || got[0].Line != "c" || got[1].Line != "d" || got[2].Line != "e" {
		t.Fatalf("entries = %v", got)
	}
	// limit truncates to the latest n entries, keeping their order
	got = l.Entries(2)
	if len(got) != 2 || got[0].Line != "d" || got[1].Line != "e" {
		t.Fatalf("limited entries = %v", got)
	}
	if l.Entries(10) == nil || len(l.Entries(10)) != 3 {
		t.Fatal("limit beyond capacity should return all")
	}
}

// TestWriteImplementsWriter covers the io.Writer adaptation (strips the trailing newline).
func TestWriteImplementsWriter(t *testing.T) {
	l := New(4)
	if n, err := l.Write([]byte("2026/10/07 log line\n")); err != nil || n != len("2026/10/07 log line\n") {
		t.Fatalf("write = %d, %v", n, err)
	}
	got := l.Entries(0)
	if len(got) != 1 || strings.HasSuffix(got[0].Line, "\n") {
		t.Fatalf("entry = %+v", got)
	}
}

// TestClear covers continuing to write after a clear.
func TestClear(t *testing.T) {
	l := New(2)
	l.Add("x")
	l.Clear()
	if got := l.Entries(0); len(got) != 0 {
		t.Fatalf("after clear entries = %v", got)
	}
	l.Add("y")
	if got := l.Entries(0); len(got) != 1 || got[0].Line != "y" {
		t.Fatalf("after refill entries = %v", got)
	}
}
