package cloudprov

import (
	"context"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/prometheus/common/expfmt"
)

// histOpCount reads a labelled histogram child's _count from its exposition.
func histOpCount(t *testing.T, op string) float64 {
	t.Helper()
	out, err := testutil.CollectAndFormat(providerCallSeconds, expfmt.TypeTextPlain,
		"ferry_provider_api_seconds")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^` +
		regexp.QuoteMeta(`ferry_provider_api_seconds_count{op="`+op+`"}`) + ` ([0-9.eE+-]+)$`)
	m := re.FindStringSubmatch(string(out))
	if m == nil {
		return 0 // series created on first observation
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("power_on count %q: %v", m[1], err)
	}
	return v
}

// TestProviderCallMetrics covers F-1811: every operation outcome is counted
// (ok/error/rate_limited/compat) and gate-passed calls observe their
// attempt-loop latency; gate refusals observe none.
func TestProviderCallMetrics(t *testing.T) {
	ctx := context.Background()

	okChild := providerCallTotal.WithLabelValues("power_on", "ok")
	errChild := providerCallTotal.WithLabelValues("power_on", "error")
	compatChild := providerCallTotal.WithLabelValues("power_on", "compat")
	gateChild := providerCallTotal.WithLabelValues("refresh_status", "rate_limited")
	okBefore, errBefore := testutil.ToFloat64(okChild), testutil.ToFloat64(errChild)
	compatBefore, gateBefore := testutil.ToFloat64(compatChild), testutil.ToFloat64(gateChild)
	countBefore := histOpCount(t, "power_on")

	// Non-retryable auth failure: counted as error, one latency observation.
	f := NewFake()
	f.failHook = func(op, id string) *Error { return NewError(ErrAuth, op, id, "401", "bad key") }
	c := NewClient(f, ClientOptions{Timeout: time.Second, MaxRetries: 3, BaseBackoff: time.Millisecond})
	if err := c.PowerOn(ctx, "r-1"); AsError(err).Type != ErrAuth {
		t.Fatalf("auth error = %v", err)
	}

	// Successful power cycle: counted as ok with its own observation.
	f2 := NewFake()
	f2.Seed(Resource{ResourceID: "r-1"})
	f2.SetCaps(Capabilities{APIVersion: "2.1", Power: true})
	c2 := NewClient(f2, ClientOptions{Timeout: time.Second, MaxRetries: 0})
	if err := c2.PowerOn(ctx, "r-1"); err != nil {
		t.Fatalf("power on: %v", err)
	}

	if got := testutil.ToFloat64(errChild) - errBefore; got != 1 {
		t.Fatalf("error counter delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(okChild) - okBefore; got != 1 {
		t.Fatalf("ok counter delta = %v, want 1", got)
	}
	if got := histOpCount(t, "power_on") - countBefore; got != 2 {
		t.Fatalf("histogram count delta = %v, want 2 (both calls passed the gates)", got)
	}

	// Below the compat floor: counted as compat, never reaches the vendor.
	f3 := NewFake()
	f3.SetCaps(Capabilities{APIVersion: "1.2", Power: true})
	c3 := NewClient(f3, ClientOptions{Timeout: time.Second, MaxRetries: 0, MinAPIVersion: "2.0"})
	if err := c3.PowerOn(ctx, "r-1"); AsError(err).Type != ErrCompat {
		t.Fatalf("compat error = %v", err)
	}
	if got := testutil.ToFloat64(compatChild) - compatBefore; got != 1 {
		t.Fatalf("compat counter delta = %v, want 1", got)
	}

	// Client-side rate limit: counted, and no latency observation happens.
	f4 := NewFake()
	f4.Seed(Resource{ResourceID: "r-1"})
	c4 := NewClient(f4, ClientOptions{Timeout: time.Second, MaxRetries: 0,
		RatePerSec: 1, Burst: 1, BaseBackoff: time.Millisecond})
	if _, err := c4.RefreshStatus(ctx, "r-1"); err != nil {
		t.Fatalf("first call within burst: %v", err)
	}
	if _, err := c4.RefreshStatus(ctx, "r-1"); AsError(err).Type != ErrRateLimited {
		t.Fatalf("second call = %v, want rate limit", err)
	}
	if got := testutil.ToFloat64(gateChild) - gateBefore; got != 1 {
		t.Fatalf("rate_limited counter delta = %v, want 1", got)
	}
}
