package announce

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeAudience(t *testing.T) {
	cases := []struct {
		in   string
		want string
		err  error
	}{
		{"", "all", nil},
		{"all", "all", nil},
		{"expiring:7", "expiring:7", nil},
		{"expiring:07", "expiring:7", nil},
		{"expiring:1", "expiring:1", nil},
		{"expiring:90", "expiring:90", nil},
		{"expiring:0", "", ErrBadAudience},
		{"expiring:91", "", ErrBadAudience},
		{"expiring:-1", "", ErrBadAudience},
		{"expiring:abc", "", ErrBadAudience},
		{"expiring:", "", ErrBadAudience},
		{"foo", "", ErrBadAudience},
		{"EXPIRING:7", "", ErrBadAudience},
	}
	for _, c := range cases {
		got, err := NormalizeAudience(c.in)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("NormalizeAudience(%q) = %q, %v; want %q, %v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestResendDedupKey(t *testing.T) {
	now := time.Date(2026, 10, 13, 14, 32, 0, 0, time.UTC)
	got := resendDedupKey(42, now)
	want := "notice_resend:42:202610131432"
	if got != want {
		t.Errorf("resendDedupKey(42, %v) = %q; want %q", now, got, want)
	}
}
