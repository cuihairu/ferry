package ringlog

import (
	"strings"
	"testing"
)

// TestRingOverwrite 覆盖环形覆盖与 Entries 时间升序、limit 截取。
func TestRingOverwrite(t *testing.T) {
	l := New(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		l.Add(s)
	}
	got := l.Entries(0)
	if len(got) != 3 || got[0].Line != "c" || got[1].Line != "d" || got[2].Line != "e" {
		t.Fatalf("entries = %v", got)
	}
	// limit 截取最近的 n 条，顺序保持
	got = l.Entries(2)
	if len(got) != 2 || got[0].Line != "d" || got[1].Line != "e" {
		t.Fatalf("limited entries = %v", got)
	}
	if l.Entries(10) == nil || len(l.Entries(10)) != 3 {
		t.Fatal("limit beyond capacity should return all")
	}
}

// TestWriteImplementsWriter 覆盖 io.Writer 适配（去尾部换行）。
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

// TestClear 覆盖清空后可继续写入。
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
