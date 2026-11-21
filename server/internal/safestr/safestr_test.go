package safestr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestHead 覆盖前缀截断三口径：未超限原样返回、截点落在多字节字符中间
// 回退到边界、整体非法 UTF-8（连续体字节）至多回退 3 字节不清空。
func TestHead(t *testing.T) {
	if got := Head("short", 255); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	if got := Head("short", 0); got != "" {
		t.Fatalf("n=0 = %q", got)
	}
	// 上限字节落在多字节字符中间（流 首字节在 253）：回退到 253。
	s := strings.Repeat("x", 253) + strings.Repeat("流", 10)
	if got := Head(s, 255); len(got) != 253 || !utf8.ValidString(got) {
		t.Fatalf("mid-rune = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// 边界恰在字符起始（85×3=255 整）：截至上限整字节。
	if got := Head(strings.Repeat("流", 100), 255); len(got) != 255 || !utf8.ValidString(got) {
		t.Fatalf("aligned = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// 非法 UTF-8（全部是延续字节 0x80-0xBF）：修复前全量回退到 0 变空串，
	// 修复后至多回退 3 字节，快照不丢。
	bad := strings.Repeat("\x80", 3000)
	got := Head(bad, 2048)
	if len(got) < 2048-utf8.UTFMax+1 || len(got) > 2048 {
		t.Fatalf("invalid utf8 head = %d bytes, want 2045..2048", len(got))
	}
}

// TestTail 覆盖后缀截断：起点落在多字节字符中间前移到边界；非法 UTF-8
// 尾段至多前移 3 字节不清空。
func TestTail(t *testing.T) {
	if got := Tail("short", 500); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	// 起点（len-500）落在「流」字符中间：前移到边界。
	s := strings.Repeat("流", 300) // 每字 3 字节，len=900
	got := Tail(s, 500)
	if !utf8.ValidString(got) || len(got) < 498 || !utf8.RuneStart(got[0]) {
		t.Fatalf("tail mid-rune = %d bytes, valid=%v, runeStart=%v", len(got), utf8.ValidString(got), len(got) > 0 && utf8.RuneStart(got[0]))
	}
	// 首字节是字符起始：原样取尾段。
	if got := Tail(s, 501); len(got) != 501 || !utf8.ValidString(got) {
		t.Fatalf("tail aligned = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// 非法 UTF-8：前移止损不清空。
	bad := strings.Repeat("\x80", 900)
	if got := Tail(bad, 500); len(got) < 500-utf8.UTFMax+1 || len(got) > 500 {
		t.Fatalf("invalid utf8 tail = %d bytes, want 497..500", len(got))
	}
	if got := Tail("abc", 0); got != "" {
		t.Fatalf("n=0 = %q", got)
	}
}

// TestHeadEllipsis 覆盖带省略号截断：超限回退边界后追加 …；未超限原样
// 返回不追加。
func TestHeadEllipsis(t *testing.T) {
	if got := HeadEllipsis("short", 10); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	got := HeadEllipsis(strings.Repeat("流", 100), 255)
	if len(got) != 255+3 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("ellipsis = %d bytes, valid=%v, suffix=%v", len(got), utf8.ValidString(got), strings.HasSuffix(got, "…"))
	}
}
