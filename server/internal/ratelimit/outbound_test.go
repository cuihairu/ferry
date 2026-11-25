package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestOutboundPacesPerHost covers F-0047: a pacer allows max calls per host
// per window, keeps other hosts independent, and blocks the next call until
// the oldest slot ages out.
type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestOutboundPacesPerHost(t *testing.T) {
	// A small window keeps the blocked-call timer real but short.
	o := NewOutbound(200*time.Millisecond, 2)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := o.Wait(ctx, "api.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	// Third call to the same host blocks; a different host proceeds immediately.
	blocked := make(chan error, 1)
	go func() { blocked <- o.Wait(ctx, "api.example.com") }()
	select {
	case err := <-blocked:
		t.Fatalf("third call did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := o.Wait(ctx, "other.example.com"); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-blocked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked call never proceeded after the window aged out")
	}
}

// TestOutboundWaitCancel covers the context contract: a saturated host with a
// cancelled context returns immediately instead of waiting a whole window.
func TestOutboundWaitCancel(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	o := NewOutbound(time.Hour, 1)
	o.now = clk.now
	if err := o.Wait(context.Background(), "slow.example.com"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Wait(ctx, "slow.example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}
}

// TestOutboundWaitURL covers host extraction: URLs sharing a remote host share
// one bucket; an unparseable value still takes a slot rather than bypassing
// pacing.
func TestOutboundWaitURL(t *testing.T) {
	saved := OutboundDefault
	OutboundDefault = NewOutbound(time.Hour, 1)
	defer func() { OutboundDefault = saved }()

	if err := WaitURL(context.Background(), "https://api.example.com/a"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := WaitURL(ctx, "https://api.example.com/b"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same host should be paced: %v", err)
	}
	if err := WaitURL(context.Background(), "not-a-url"); err != nil {
		t.Fatalf("raw key should take a slot: %v", err)
	}
}
