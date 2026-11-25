package safestr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestHead covers the three prefix-truncation models: an under-limit input is returned as-is, a cut point inside a
// multibyte rune backs off to the boundary, and entirely invalid UTF-8 (a run of continuation bytes) backs off
func TestHead(t *testing.T) {
	if got := Head("short", 255); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	if got := Head("short", 0); got != "" {
		t.Fatalf("n=0 = %q", got)
	}
	// The limit lands inside a multibyte rune (the first rune starts at byte 253): back off to 253.
	s := strings.Repeat("x", 253) + strings.Repeat("流", 10)
	if got := Head(s, 255); len(got) != 253 || !utf8.ValidString(got) {
		t.Fatalf("mid-rune = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// The boundary lands exactly on a rune start (85×3=255 exact): cut to the limit in whole bytes.
	if got := Head(strings.Repeat("流", 100), 255); len(got) != 255 || !utf8.ValidString(got) {
		t.Fatalf("aligned = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// Invalid UTF-8 (all continuation bytes 0x80-0xBF): before the fix a full back-off to 0 produced an empty string;
	// after the fix at most 3 bytes back off, so the snapshot is not lost.
	bad := strings.Repeat("\x80", 3000)
	got := Head(bad, 2048)
	if len(got) < 2048-utf8.UTFMax+1 || len(got) > 2048 {
		t.Fatalf("invalid utf8 head = %d bytes, want 2045..2048", len(got))
	}
}

// TestTail covers suffix truncation: a start inside a multibyte rune moves forward to the boundary; an invalid UTF-8
// tail moves forward at most 3 bytes without blanking out.
func TestTail(t *testing.T) {
	if got := Tail("short", 500); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	// The start (len-500) lands inside a 3-byte rune: move forward to the boundary.
	s := strings.Repeat("流", 300) // 3 bytes per rune, len=900
	got := Tail(s, 500)
	if !utf8.ValidString(got) || len(got) < 498 || !utf8.RuneStart(got[0]) {
		t.Fatalf("tail mid-rune = %d bytes, valid=%v, runeStart=%v", len(got), utf8.ValidString(got), len(got) > 0 && utf8.RuneStart(got[0]))
	}
	// The first byte is a rune start: take the tail as-is.
	if got := Tail(s, 501); len(got) != 501 || !utf8.ValidString(got) {
		t.Fatalf("tail aligned = %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	// Invalid UTF-8: the forward back-off stops without blanking out.
	bad := strings.Repeat("\x80", 900)
	if got := Tail(bad, 500); len(got) < 500-utf8.UTFMax+1 || len(got) > 500 {
		t.Fatalf("invalid utf8 tail = %d bytes, want 497..500", len(got))
	}
	if got := Tail("abc", 0); got != "" {
		t.Fatalf("n=0 = %q", got)
	}
}

// TestHeadEllipsis covers ellipsis truncation: over the limit it backs off to the boundary and appends …; under
// the limit it is returned as-is with nothing appended.
func TestHeadEllipsis(t *testing.T) {
	if got := HeadEllipsis("short", 10); got != "short" {
		t.Fatalf("passthrough = %q", got)
	}
	got := HeadEllipsis(strings.Repeat("流", 100), 255)
	if len(got) != 255+3 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("ellipsis = %d bytes, valid=%v, suffix=%v", len(got), utf8.ValidString(got), strings.HasSuffix(got, "…"))
	}
}
