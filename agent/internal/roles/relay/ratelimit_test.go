package relay

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
)

// TestRateReaderBucket covers F-0810's token accounting with a controllable
// clock: a full bucket permits an immediate read, a second read exceeding the
// remaining tokens computes a wait, and idle time refills up to the burst cap.
func TestRateReaderBucket(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	src := strings.NewReader(strings.Repeat("x", 4096))
	r := &rateReader{
		src: src, rate: 1000, burst: 1000, tokens: 1000,
		last: now, now: func() time.Time { return now },
	}
	buf := make([]byte, 600)
	n, err := r.Read(buf)
	if err != nil || n != 600 {
		t.Fatalf("first read: n=%d err=%v", n, err)
	}
	if wait := r.readAllowed(600); wait <= 0 {
		t.Fatalf("second read must wait: %v", wait)
	}
	// One second of idle time refills 1000 bytes, enough for the next read.
	now = now.Add(time.Second)
	if wait := r.readAllowed(600); wait != 0 {
		t.Fatalf("refilled bucket must allow read immediately: %v", wait)
	}
	// Long idle time never refills past burst: a request larger than the burst
	// still needs a wait proportional to the excess.
	now = now.Add(10 * time.Second)
	if wait := r.readAllowed(1001); wait <= 0 {
		t.Fatalf("burst cap must limit refill: %v", wait)
	}
}

// TestRateReaderUnlimited covers the disabled case: no wrapper reader is
// returned when the rate is zero or negative.
func TestRateReaderUnlimited(t *testing.T) {
	src := bytes.NewReader([]byte("abc"))
	if got := newRateReader(src, 0, time.Now); got != src {
		t.Fatal("zero rate must keep the source unwrapped")
	}
	if got := newRateReader(src, -1, time.Now); got != src {
		t.Fatal("negative rate must keep the source unwrapped")
	}
}

// TestEffectiveRateKbps covers config normalization and hot-merge (F-0810).
func TestEffectiveRateKbps(t *testing.T) {
	if got := effectiveRateKbps(config.RelaySpec{RateLimitKbps: -5}); got != 0 {
		t.Fatalf("negative rate = %d; want 0", got)
	}
	if got := effectiveRateKbps(config.RelaySpec{RateLimitKbps: 800}); got != 800 {
		t.Fatalf("rate = %d; want 800", got)
	}
	merged := mergeSpec(config.RelaySpec{RateLimitKbps: 100}, config.RelaySpec{RateLimitKbps: 400})
	if got := effectiveRateKbps(merged); got != 400 {
		t.Fatalf("merged rate = %d; want 400", got)
	}
}
