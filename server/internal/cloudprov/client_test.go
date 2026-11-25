package cloudprov

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestClientRetryPolicy covers F-0316's retry half: retryable types are
// reattempted with backoff until success; non-retryable types surface at
// once; exhaustion returns the last error.
func TestClientRetryPolicy(t *testing.T) {
	ctx := context.Background()

	// Rate-limited twice, then success: 3 attempts.
	f := NewFake()
	n := 0
	f.failHook = func(op, id string) *Error {
		n++
		if n <= 2 {
			return NewError(ErrRateLimited, op, id, "429", "slow down")
		}
		return nil
	}
	c := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 3, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond})
	if _, err := c.ListResources(ctx, Filter{}); err != nil {
		t.Fatalf("retry-until-success: %v", err)
	}
	if calls := len(f.Calls()); calls != 3 {
		t.Fatalf("attempts = %d, want 3", calls)
	}

	// Auth failure never retries.
	f2 := NewFake()
	f2.failHook = func(op, id string) *Error { return NewError(ErrAuth, op, id, "401", "bad key") }
	c2 := NewClient(f2, ClientOptions{Timeout: time.Second, MaxRetries: 3, BaseBackoff: time.Millisecond})
	if _, err := c2.ListResources(ctx, Filter{}); AsError(err).Type != ErrAuth {
		t.Fatalf("auth error = %v", err)
	}
	if calls := len(f2.Calls()); calls != 1 {
		t.Fatalf("auth attempts = %d, want 1", calls)
	}

	// Exhaustion returns the last retryable error.
	f3 := NewFake()
	f3.failHook = func(op, id string) *Error { return NewError(ErrUnavailable, op, id, "503", "down") }
	c3 := NewClient(f3, ClientOptions{Timeout: time.Second, MaxRetries: 2, BaseBackoff: time.Millisecond})
	err := c3.PowerOn(ctx, "r-1")
	if AsError(err).Type != ErrUnavailable {
		t.Fatalf("exhausted error = %v", err)
	}
	if calls := len(f3.Calls()); calls != 3 {
		t.Fatalf("exhausted attempts = %d, want 3 (1+2 retries)", calls)
	}
}

// TestClientTimeout covers F-0316's timeout half: a hung underlying call is
// cut at the per-call deadline and reported as Unavailable.
func TestClientTimeout(t *testing.T) {
	slow := slowAdapter{delay: 200 * time.Millisecond}
	c := NewClient(slow, ClientOptions{Timeout: 20 * time.Millisecond, MaxRetries: 0})
	start := time.Now()
	err := c.PowerOn(context.Background(), "r-1")
	if AsError(err).Type != ErrUnavailable {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("timeout took %v, deadline not enforced", elapsed)
	}
}

// slowAdapter sleeps past any short deadline before succeeding.
type slowAdapter struct{ delay time.Duration }

func (s slowAdapter) Capabilities(ctx context.Context) (Capabilities, error) {
	return Capabilities{APIVersion: "2.0"}, nil
}
func (s slowAdapter) ListResources(ctx context.Context, f Filter) ([]Resource, error) {
	return nil, nil
}
func (s slowAdapter) DescribeResource(ctx context.Context, id string) (*Resource, error) {
	return nil, nil
}
func (s slowAdapter) RefreshStatus(ctx context.Context, id string) (string, error) { return "", nil }
func (s slowAdapter) PowerOn(ctx context.Context, id string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.delay):
		return nil
	}
}
func (s slowAdapter) PowerOff(ctx context.Context, id string) error    { return nil }
func (s slowAdapter) Reboot(ctx context.Context, id string) error      { return nil }
func (s slowAdapter) ForceStop(ctx context.Context, id string) error   { return nil }
func (s slowAdapter) Rebuild(ctx context.Context, id, im string) error { return nil }
func (s slowAdapter) ImportBilling(ctx context.Context, w BillingWindow) ([]BillingRecord, error) {
	return nil, nil
}

// TestClientRateLimit covers F-0316's rate-limit half: over-quota instants
// are rejected with a client-side rate_limited error, not passed through.
func TestClientRateLimit(t *testing.T) {
	f := NewFake()
	f.Seed(Resource{ResourceID: "r-1"})
	c := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 0,
		RatePerSec: 1, Burst: 1, BaseBackoff: time.Millisecond})
	ctx := context.Background()
	if _, err := c.RefreshStatus(ctx, "r-1"); err != nil {
		t.Fatalf("first call within burst: %v", err)
	}
	if _, err := c.RefreshStatus(ctx, "r-1"); AsError(err).Type != ErrRateLimited {
		t.Fatalf("second immediate call = %v, want client rate limit", err)
	}
	// After the refill window the token is back.
	time.Sleep(1100 * time.Millisecond)
	if _, err := c.RefreshStatus(ctx, "r-1"); err != nil {
		t.Fatalf("refilled call: %v", err)
	}
}

// TestVersionCompat covers F-0317: dotted-version comparison and the
// client-side floor.
func TestVersionCompat(t *testing.T) {
	cases := []struct {
		got, want string
		ok        bool
	}{
		{"2.1", "1.0", true},
		{"2.1", "2.1", true},
		{"2.1", "2.2", false},
		{"2.10", "2.9", true},  // numeric, not lexicographic
		{"2", "1.9.3", true},   // shorter got fills zeros
		{"1.9.3", "2", false},  //
		{"", "1.0", false},     // malformed fails closed
		{"x.y", "1.0", false},  // non-numeric fails closed
		{"3.0.1", "3.0", true}, //
	}
	for _, c := range cases {
		if got := VersionAtLeast(c.got, c.want); got != c.ok {
			t.Fatalf("VersionAtLeast(%q, %q) = %v", c.got, c.want, got)
		}
	}

	// Client floor: an adapter below the floor fails every call with
	// ErrCompat; an adapter at/above passes.
	f := NewFake()
	f.SetCaps(Capabilities{APIVersion: "1.2", Power: true})
	c := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 0, MinAPIVersion: "2.0"})
	err := c.PowerOn(context.Background(), "r-1")
	if AsError(err).Type != ErrCompat {
		t.Fatalf("below-floor error = %v", err)
	}
	if len(f.Calls()) != 0 {
		t.Fatal("below-floor call reached the adapter")
	}
	f.SetCaps(Capabilities{APIVersion: "2.1", Power: true})
	f.Seed(Resource{ResourceID: "r-1"})
	c2 := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 0, MinAPIVersion: "2.0"})
	if err := c2.PowerOn(context.Background(), "r-1"); err != nil {
		t.Fatalf("at-floor call: %v", err)
	}
}

// TestClientTypedErrors passes typed errors through the decorator untouched
// (same *Error identity semantics the handler layer will branch on).
func TestClientTypedErrors(t *testing.T) {
	f := NewFake()
	f.failHook = func(op, id string) *Error { return NewError(ErrNotFound, op, id, "gone", "no such resource") }
	c := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 0})
	_, err := c.DescribeResource(context.Background(), "missing")
	e := AsError(err)
	if e.Type != ErrNotFound || e.Op != OpDescribe || e.ProviderCode != "gone" {
		t.Fatalf("typed error = %+v", e)
	}
	if !errors.Is(err, err) { // sanity: errors package interop compiles
		t.Fatal("unreachable")
	}
}
