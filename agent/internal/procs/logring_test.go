//go:build unix

package procs

import (
	"testing"
)

// TestLogRingSnapshot 覆盖环形覆盖、limit 截取、去换行与空行跳过。
func TestLogRingSnapshot(t *testing.T) {
	r := newLogRing(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		if n, err := r.Write([]byte(s + "\n")); err != nil || n != len(s)+1 {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	// 容量 3：只留最近三条，时间升序
	got := r.snapshot(0)
	if len(got) != 3 || got[0] != "c" || got[1] != "d" || got[2] != "e" {
		t.Fatalf("snapshot = %v", got)
	}
	// limit 截取最近的 n 条
	got = r.snapshot(2)
	if len(got) != 2 || got[0] != "d" || got[1] != "e" {
		t.Fatalf("limited snapshot = %v", got)
	}
	// 空行与仅换行不入环
	if _, err := r.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if len(r.snapshot(0)) != 3 {
		t.Fatalf("empty line should not be recorded: %v", r.snapshot(0))
	}
	// 管道复制可能把多行并成一次写入：按内嵌换行拆行
	if _, err := r.Write([]byte("x\ny\n")); err != nil {
		t.Fatal(err)
	}
	got = r.snapshot(2)
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("multiline write snapshot = %v", got)
	}
	// CRLF 归一
	if _, err := r.Write([]byte("win\r\n")); err != nil {
		t.Fatal(err)
	}
	got = r.snapshot(1)
	if len(got) != 1 || got[0] != "win" {
		t.Fatalf("crlf snapshot = %v", got)
	}
}
