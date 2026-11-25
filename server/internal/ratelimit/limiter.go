// Package ratelimit is an in-memory sliding-window rate limiter (PAY-5):
// per-key (such as a client IP) attempt caps inside a window plus failure lockout, with zero external dependencies, suited to a single host with modest memory.
package ratelimit

import (
	"sync"
	"time"
)

// Options are the rate-limiting parameters.
type Options struct {
	// Window is the width of the sliding window.
	Window time.Duration
	// MaxAttempts is the number of attempts allowed inside the window.
	MaxAttempts int
	// FailLimit is the number of failures inside the window that triggers lockout.
	FailLimit int
	// Lockout is the lockout duration.
	Lockout time.Duration
}

type state struct {
	attempts    []time.Time
	failures    []time.Time
	lockedUntil time.Time
}

// Limiter rate-limits per key. All methods are concurrency-safe.
type Limiter struct {
	mu   sync.Mutex
	opts Options
	now  func() time.Time
	keys map[string]*state
}

// New creates a limiter.
func New(o Options) *Limiter {
	return &Limiter{opts: o, now: time.Now, keys: map[string]*state{}}
}

// Allow decides one attempt: if the window is not full and the key is not locked, it takes a quota slot and allows it.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.keys[key]
	if st == nil {
		st = &state{}
		l.keys[key] = st
	}
	if now.Before(st.lockedUntil) {
		return false
	}
	st.attempts = prune(st.attempts, now.Add(-l.opts.Window))
	if len(st.attempts) >= l.opts.MaxAttempts {
		return false
	}
	st.attempts = append(st.attempts, now)
	// When the number of keys grows, also sweep once (traffic at the redemption entry point is low, so lazy cleanup suffices).
	if len(l.keys) > 4096 {
		l.sweepLocked(now)
	}
	return true
}

// RecordFailure records one failure; once failures inside the window reach FailLimit the key is locked.
func (l *Limiter) RecordFailure(key string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.keys[key]
	if st == nil {
		st = &state{}
		l.keys[key] = st
	}
	st.failures = prune(st.failures, now.Add(-l.opts.Window))
	st.failures = append(st.failures, now)
	if len(st.failures) >= l.opts.FailLimit {
		st.lockedUntil = now.Add(l.opts.Lockout)
		st.failures = nil
	}
}

// Reset clears all state of the key (called after a successful redemption).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}

func (l *Limiter) sweepLocked(now time.Time) {
	for k, st := range l.keys {
		st.attempts = prune(st.attempts, now.Add(-l.opts.Window))
		st.failures = prune(st.failures, now.Add(-l.opts.Window))
		if len(st.attempts) == 0 && len(st.failures) == 0 && now.After(st.lockedUntil) {
			delete(l.keys, k)
		}
	}
}

// prune discards stale timestamps outside the window (the input is time-ordered).
func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	if i == 0 {
		return ts
	}
	return append(ts[:0], ts[i:]...) // compact in place, reusing the capacity
}
