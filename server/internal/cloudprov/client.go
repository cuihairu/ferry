package cloudprov

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClientOptions is the shared provider-API discipline (F-0316): a per-call
// timeout, an optional client-side rate limit, and bounded retry with
// backoff for retryable error types (F-0315's Retryable). Zero fields fall
// back to the Defaults. MinAPIVersion (F-0317) optionally pins the vendor
// API floor: the first call fails with ErrCompat when the adapter reports an
// older version.
type ClientOptions struct {
	// Timeout bounds one underlying call (default 30s).
	Timeout time.Duration
	// MaxRetries is how many times a retryable failure is reattempted.
	// 0 = no retry; use Defaults.MaxRetries for the shared discipline.
	MaxRetries int
	// BaseBackoff is the first retry delay; doubles per attempt with a
	// MaxBackoff cap (default 200ms, cap 5s).
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// RatePerSec and Burst shape a simple token-bucket limiter shared by all
	// calls of this client (0 = unlimited). Deliberately dependency-free:
	// the discipline here is "protect the vendor quota", not precise pacing.
	RatePerSec float64
	Burst      int
	// MinAPIVersion fails all calls when Capabilities.APIVersion is older
	// (F-0317). Empty = no check.
	MinAPIVersion string
	// Account carries the adapter identity (provider id/name/type) for the
	// auth-failure alert channel (F-1828): identity only, never a credential.
	Account Account
}

// Defaults fills the zero-value policy (exported so handlers and future
// vendor adapters share one vocabulary).
var Defaults = ClientOptions{
	Timeout:     30 * time.Second,
	MaxRetries:  2,
	BaseBackoff: 200 * time.Millisecond,
	MaxBackoff:  5 * time.Second,
}

// Client decorates any Adapter with the F-0316 policy. It implements Adapter
// itself, so callers wire it in transparently:
//
//	var _ Adapter = (*Client)(nil)
type Client struct {
	inner Adapter
	opts  ClientOptions

	mu       sync.Mutex
	tokens   float64
	lastFill time.Time

	compatOnce sync.Once
	compatErr  error
}

// NewClient wraps a with the policy.
func NewClient(a Adapter, o ClientOptions) *Client {
	if o.Timeout <= 0 {
		o.Timeout = Defaults.Timeout
	}
	if o.MaxRetries < 0 {
		o.MaxRetries = 0
	}
	// MaxRetries is honored as-is: 0 = no retry; callers wanting the shared
	// discipline set MaxRetries: Defaults.MaxRetries (documented below).
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = Defaults.BaseBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = Defaults.MaxBackoff
	}
	if o.RatePerSec > 0 && o.Burst <= 0 {
		o.Burst = 1
	}
	return &Client{inner: a, opts: o, tokens: float64(o.Burst)}
}

var _ Adapter = (*Client)(nil)

// allow spends one token; false = rate limited this instant.
func (c *Client) allow() bool {
	if c.opts.RatePerSec <= 0 {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if !c.lastFill.IsZero() {
		c.tokens += now.Sub(c.lastFill).Seconds() * c.opts.RatePerSec
		if c.tokens > float64(c.opts.Burst) {
			c.tokens = float64(c.opts.Burst)
		}
	}
	c.lastFill = now
	if c.tokens >= 1 {
		c.tokens--
		return true
	}
	return false
}

// checkCompat runs the F-0317 floor once and caches the verdict.
func (c *Client) checkCompat(ctx context.Context) error {
	c.compatOnce.Do(func() {
		if c.opts.MinAPIVersion == "" {
			return
		}
		caps, err := c.inner.Capabilities(ctx)
		if err != nil {
			c.compatErr = err
			return
		}
		if !VersionAtLeast(caps.APIVersion, c.opts.MinAPIVersion) {
			c.compatErr = NewError(ErrCompat, OpCapabilities, "", caps.APIVersion,
				"api version "+caps.APIVersion+" is below the required "+c.opts.MinAPIVersion)
		}
	})
	return c.compatErr
}

// call runs one operation under the shared policy: compat floor (F-0317),
// rate limit, per-call timeout, bounded retry on retryable errors (F-0316).
// Gate refusals and outcomes are counted (F-1811); latency is observed only
// for calls that reached the attempt loop.
func (c *Client) call(ctx context.Context, op string, resourceID string, fn func(ctx context.Context) error) error {
	if err := c.checkCompat(ctx); err != nil {
		recordGate(op, "compat")
		return err
	}
	if !c.allow() {
		recordGate(op, "rate_limited")
		return NewError(ErrRateLimited, op, resourceID, "client_rate_limited", "client-side rate limit")
	}
	d, err := c.attempt(ctx, op, resourceID, fn)
	if err != nil {
		recordCall(op, "error", d)
		// A credentials rejection is reported for the auth alert (F-1828);
		// non-auth failures (and unset hooks) fall through as a no-op.
		ReportAuthFailure(c.opts.Account, op, resourceID, err)
		return err
	}
	recordCall(op, "ok", d)
	return nil
}

// attempt runs the bounded retry loop (F-0316) and reports the wall time of
// the whole loop (attempts plus backoff).
func (c *Client) attempt(ctx context.Context, op string, resourceID string, fn func(ctx context.Context) error) (time.Duration, error) {
	start := time.Now()
	maxRetries := c.opts.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	backoff := c.opts.BaseBackoff
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoff); err != nil {
				return time.Since(start), lastErr // caller cancelled during backoff: report the real failure
			}
			next := time.Duration(float64(backoff) * 2)
			if next > c.opts.MaxBackoff {
				next = c.opts.MaxBackoff
			}
			backoff = next
		}
		cctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
		err := fn(cctx)
		cancel()
		if err == nil {
			return time.Since(start), nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			// The per-call deadline cut the vendor call: a transport-level
			// failure, retryable like a 5xx (F-0316).
			err = NewError(ErrUnavailable, op, resourceID, "deadline", err.Error())
		}
		lastErr = err
		if !AsError(err).Type.Retryable() {
			return time.Since(start), err
		}
	}
	return time.Since(start), lastErr
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// VersionAtLeast reports whether the dotted-numeric version got is at least
// want (F-0317). Non-numeric parts compare as zero; a malformed got fails
// closed (false). "2" >= "1.9.3"; "2.1" < "2.10".
func VersionAtLeast(got, want string) bool {
	g := strings.Split(strings.TrimSpace(got), ".")
	w := strings.Split(strings.TrimSpace(want), ".")
	if len(g) == 0 || g[0] == "" {
		return false
	}
	for i := range w {
		gn, wn := 0, 0
		if i < len(g) {
			gn = atoiSegment(g[i])
		}
		wn = atoiSegment(w[i])
		if gn != wn {
			return gn > wn
		}
	}
	return true
}

func atoiSegment(s string) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && v >= 0 {
		return v
	}
	return 0
}

func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var caps Capabilities
	err := c.call(ctx, OpCapabilities, "", func(ctx context.Context) error {
		var e error
		caps, e = c.inner.Capabilities(ctx)
		return e
	})
	return caps, err
}

func (c *Client) ListResources(ctx context.Context, f Filter) ([]Resource, error) {
	var out []Resource
	err := c.call(ctx, OpListResources, "", func(ctx context.Context) error {
		var e error
		out, e = c.inner.ListResources(ctx, f)
		return e
	})
	return out, err
}

func (c *Client) DescribeResource(ctx context.Context, resourceID string) (*Resource, error) {
	var out *Resource
	err := c.call(ctx, OpDescribe, resourceID, func(ctx context.Context) error {
		var e error
		out, e = c.inner.DescribeResource(ctx, resourceID)
		return e
	})
	return out, err
}

func (c *Client) RefreshStatus(ctx context.Context, resourceID string) (string, error) {
	var state string
	err := c.call(ctx, OpRefreshStatus, resourceID, func(ctx context.Context) error {
		var e error
		state, e = c.inner.RefreshStatus(ctx, resourceID)
		return e
	})
	return state, err
}

func (c *Client) PowerOn(ctx context.Context, resourceID string) error {
	return c.call(ctx, OpPowerOn, resourceID, func(ctx context.Context) error {
		return c.inner.PowerOn(ctx, resourceID)
	})
}

func (c *Client) PowerOff(ctx context.Context, resourceID string) error {
	return c.call(ctx, OpPowerOff, resourceID, func(ctx context.Context) error {
		return c.inner.PowerOff(ctx, resourceID)
	})
}

func (c *Client) Reboot(ctx context.Context, resourceID string) error {
	return c.call(ctx, OpReboot, resourceID, func(ctx context.Context) error {
		return c.inner.Reboot(ctx, resourceID)
	})
}

func (c *Client) ForceStop(ctx context.Context, resourceID string) error {
	return c.call(ctx, OpForceStop, resourceID, func(ctx context.Context) error {
		return c.inner.ForceStop(ctx, resourceID)
	})
}

func (c *Client) Rebuild(ctx context.Context, resourceID, image string) error {
	return c.call(ctx, OpRebuild, resourceID, func(ctx context.Context) error {
		return c.inner.Rebuild(ctx, resourceID, image)
	})
}

func (c *Client) ImportBilling(ctx context.Context, w BillingWindow) ([]BillingRecord, error) {
	var out []BillingRecord
	err := c.call(ctx, OpImportBilling, "", func(ctx context.Context) error {
		var e error
		out, e = c.inner.ImportBilling(ctx, w)
		return e
	})
	return out, err
}
