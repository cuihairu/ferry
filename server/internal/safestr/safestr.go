// Package safestr provides truncation safe for UTF-8 character boundaries (codex deep review P2 fix):
// a hard byte cut slices multibyte characters in half, leaving invalid UTF-8 once persisted (snapshots/error texts may contain Chinese);
// but falling back to the character boundary wholesale would blank out inputs that are "entirely invalid" (binary payloads).
// Three models: Head prefix truncation, Tail suffix truncation, HeadEllipsis prefix truncation with an ellipsis.
package safestr

import "unicode/utf8"

// Head takes up to n bytes of the prefix of s, backing the cut point off to a character boundary. For n<=0 or an s
// not over the limit, see the implementation: an under-limit input is returned as-is; n<=0 returns the empty string.
// If the input is entirely invalid UTF-8 (e.g. a binary payload) it backs off at most utf8.UTFMax-1 bytes and stops —
// backing off further would leave the snapshot completely lost as an empty string.
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for i := 0; i < utf8.UTFMax-1 && n > 0 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n]
}

// Tail takes up to n bytes of the suffix of s, moving the start forward to a character boundary (error summaries
// usually take the tail, whose first byte may be the second half of a multibyte rune). Invalid UTF-8 input moves forward
// at most utf8.UTFMax-1 bytes and stops, so the tail is never blanked out.
func Tail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for i := 0; i < utf8.UTFMax-1 && start < len(s) && !utf8.RuneStart(s[start]); i++ {
		start++
	}
	return s[start:]
}

// HeadEllipsis takes up to n bytes of the prefix of s and appends an ellipsis, backing the cut point off to a character boundary
// (DNS provider error summary model: truncated output ends with … to signal omission).
func HeadEllipsis(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return Head(s, n) + "…"
}
