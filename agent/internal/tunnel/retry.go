package tunnel

import (
	"context"
	"fmt"
	"net"
	"time"
)

// F-0824: one reconnect policy for every plugin. The per-attempt timeout is
// each plugin's Options.TimeoutOrDefault (already uniform); what was missing
// is a shared answer to "a dial failed -- try again?": config and auth
// failures cannot succeed by retrying unchanged options, transport trouble
// can.

// Defaults for RetryOptions zero fields.
const (
	DefaultAttempts = 1
	DefaultBackoff  = 200 * time.Millisecond
)

// RetryOptions tunes DialRetry.
type RetryOptions struct {
	// Attempts is the total number of dial tries (1 = a single try); <=0 takes DefaultAttempts.
	Attempts int
	// Backoff is the pause before the second and later attempts; <=0 takes DefaultBackoff.
	Backoff time.Duration
	// Timeout caps each attempt; <=0 leaves the plugin's own Options timeout in charge.
	Timeout time.Duration
	// OnFail runs after every failed attempt (relay evicts its cached tunnel here).
	OnFail func(err error)
}

// DialError reports a dial that gave up: the address, how many attempts ran,
// and the last failure. Classify still reads the class from Err.
type DialError struct {
	Addr     string
	Attempts int
	Err      error
}

func (e *DialError) Error() string {
	return fmt.Sprintf("dial %s after %d attempts: %v", e.Addr, e.Attempts, e.Err)
}

func (e *DialError) Unwrap() error { return e.Err }

// Retryable reports whether a failed dial is worth another attempt: config
// and auth failures are not (retrying cannot fix bad options or a refusing
// peer); everything else -- network, heartbeat, unclassified third-party
// errors -- may succeed on a later try.
func Retryable(err error) bool {
	switch Classify(err) {
	case ClassConfig, ClassAuth:
		return false
	}
	return true
}

// DialRetry dials through a plugin with the unified reconnect behavior
// (F-0824): each attempt carries Timeout (or the plugin's own), transport
// failures retry up to Attempts-1 more times after Backoff pauses, and
// config/auth failures give up immediately. The returned error is always a
// *DialError, so callers log attempts consistently.
func DialRetry(ctx context.Context, tn Tunnel, addr string, opts RetryOptions) (net.Conn, error) {
	if opts.Attempts <= 0 {
		opts.Attempts = DefaultAttempts
	}
	if opts.Backoff <= 0 {
		opts.Backoff = DefaultBackoff
	}
	for attempt := 1; ; attempt++ {
		dialCtx := ctx
		var cancel context.CancelFunc
		if opts.Timeout > 0 {
			dialCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		}
		conn, err := tn.Dial(dialCtx, addr)
		if cancel != nil {
			cancel()
		}
		if err == nil {
			return conn, nil
		}
		if opts.OnFail != nil {
			opts.OnFail(err)
		}
		if attempt >= opts.Attempts || !Retryable(err) {
			return nil, &DialError{Addr: addr, Attempts: attempt, Err: err}
		}
		timer := time.NewTimer(opts.Backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &DialError{Addr: addr, Attempts: attempt, Err: ctx.Err()}
		case <-timer.C:
		}
	}
}
