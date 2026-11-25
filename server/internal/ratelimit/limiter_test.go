package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

func TestSlidingWindow(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 3, FailLimit: 100, Lockout: time.Minute})
	l.now = func() time.Time { return base }

	for i := 0; i < 3; i++ {
		if !l.Allow("ip") {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	if l.Allow("ip") {
		t.Fatal("4th attempt in window should block")
	}
	// Once the window has slid past, requests are allowed again
	l.now = func() time.Time { return base.Add(61 * time.Second) }
	if !l.Allow("ip") {
		t.Fatal("after window should pass")
	}
	// Different keys do not affect each other
	l.now = func() time.Time { return base }
	if !l.Allow("other") {
		t.Fatal("other key should pass")
	}
}

func TestFailLockout(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 100, FailLimit: 3, Lockout: 15 * time.Minute})
	l.now = func() time.Time { return base }

	if !l.Allow("ip") {
		t.Fatal("should pass initially")
	}
	l.RecordFailure("ip")
	l.RecordFailure("ip")
	if !l.Allow("ip") {
		t.Fatal("below fail threshold should still pass")
	}
	l.RecordFailure("ip") // the 3rd failure -> locked
	if l.Allow("ip") {
		t.Fatal("locked key should block")
	}
	// Attempts during lockout do not change the outcome; the lock expires and the key recovers
	l.now = func() time.Time { return base.Add(14 * time.Minute) }
	if l.Allow("ip") {
		t.Fatal("within lockout should block")
	}
	l.now = func() time.Time { return base.Add(16 * time.Minute) }
	if !l.Allow("ip") {
		t.Fatal("after lockout should pass")
	}
}

func TestReset(t *testing.T) {
	base := time.Now()
	l := New(Options{Window: time.Minute, MaxAttempts: 1, FailLimit: 1, Lockout: time.Hour})
	l.now = func() time.Time { return base }

	if !l.Allow("ip") {
		t.Fatal("first should pass")
	}
	if l.Allow("ip") {
		t.Fatal("window exhausted")
	}
	l.RecordFailure("ip") // triggers the lockout
	if l.Allow("ip") {
		t.Fatal("locked")
	}
	l.Reset("ip")
	if !l.Allow("ip") {
		t.Fatal("reset should clear window and lock")
	}
}

// TestSweepLockedDropsStaleKeys crossing the key-count limit triggers a lazy sweep: keys outside the window
// and not locked are dropped, while locked keys and new keys are kept.
func TestSweepLockedDropsStaleKeys(t *testing.T) {
	base := time.Now()
	var now time.Time = base
	l := New(Options{Window: time.Minute, MaxAttempts: 10, FailLimit: 1, Lockout: time.Minute})
	l.now = func() time.Time { return now }

	// Fill 4097 keys (each taking one slot inside a window).
	for i := 0; i < 4097; i++ {
		if !l.Allow(fmt.Sprintf("k%d", i)) {
			t.Fatalf("key %d rejected", i)
		}
	}
	// Advance the clock and then lock one key (the locked state is not affected by out-of-window cleanup).
	now = base.Add(2 * time.Minute)
	l.RecordFailure("keep")
	// A new key triggers the sweep.
	if !l.Allow("newkey") {
		t.Fatal("newkey should be allowed")
	}
	if got := len(l.keys); got != 2 {
		t.Fatalf("keys after sweep = %d; want 2 (keep + newkey)", got)
	}
	// A locked key is still rejected.
	if l.Allow("keep") {
		t.Fatal("locked key should be rejected")
	}
}
