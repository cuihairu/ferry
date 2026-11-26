package tunnel

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// failThenSucceed fails the first N dials with the given errors, then returns
// a pipe conn.
type failThenSucceed struct {
	Tunnel
	name   string
	fail   int
	errs   []error
	calls  int
	failed []error
}

func (f *failThenSucceed) Name() string { return f.name }
func (f *failThenSucceed) Dial(ctx context.Context, addr string) (net.Conn, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) {
		f.failed = append(f.failed, f.errs[i])
		return nil, f.errs[i]
	}
	c1, c2 := net.Pipe()
	go func() { _ = c1.Close() }()
	return c2, nil
}

// TestDialRetryClassification covers F-0824's policy: network failures retry
// to the attempt budget, config/auth failures stop immediately, and the
// result error always reports the attempts that ran.
func TestDialRetryClassification(t *testing.T) {
	// two network failures then success: 3 attempts total
	tn := &failThenSucceed{errs: []error{
		Wrap(ClassNetwork, "x:dial", errors.New("refused")),
		Wrap(ClassNetwork, "x:dial", errors.New("reset")),
	}}
	conn, err := DialRetry(context.Background(), tn, "1.2.3.4:5", RetryOptions{Attempts: 3})
	if err != nil {
		t.Fatalf("retry to success: %v", err)
	}
	_ = conn.Close()
	if tn.calls != 3 {
		t.Fatalf("dials = %d, want 3", tn.calls)
	}

	// config failure on the first try: no second attempt
	tnCfg := &failThenSucceed{errs: []error{Wrap(ClassConfig, "x:dial", errors.New("bad key"))}}
	_, err = DialRetry(context.Background(), tnCfg, "1.2.3.4:5", RetryOptions{Attempts: 3})
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("want *DialError, got %T (%v)", err, err)
	}
	if de.Attempts != 1 || tnCfg.calls != 1 {
		t.Fatalf("config failure retried: attempts=%d dials=%d", de.Attempts, tnCfg.calls)
	}

	// auth failure behaves the same
	tnAuth := &failThenSucceed{errs: []error{Wrap(ClassAuth, "x:dial", errors.New("ERR no"))}}
	_, err = DialRetry(context.Background(), tnAuth, "1.2.3.4:5", RetryOptions{Attempts: 5})
	if !errors.As(err, &de) || de.Attempts != 1 {
		t.Fatalf("auth failure attempts = %v", err)
	}

	// exhausted network failures report the full budget
	tnAll := &failThenSucceed{errs: []error{
		errors.New("e1"), errors.New("e2"),
	}}
	_, err = DialRetry(context.Background(), tnAll, "1.2.3.4:5", RetryOptions{Attempts: 2})
	if !errors.As(err, &de) || de.Attempts != 2 {
		t.Fatalf("exhausted attempts = %v", err)
	}
	if !Retryable(err) {
		t.Fatalf("exhausted network errors stay retryable: %v", err)
	}

	// ctx cancelled during the backoff window stops the loop
	tCtx := &failThenSucceed{errs: []error{errors.New("down")}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err = DialRetry(ctx, tCtx, "1.2.3.4:5", RetryOptions{Attempts: 5, Backoff: 2 * time.Second})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("ctx cancel during backoff: err=%v elapsed=%v", err, time.Since(start))
	}
}

// TestDialRetryDefaults covers the zero-option normalization and the OnFail hook.
func TestDialRetryDefaults(t *testing.T) {
	tn := &failThenSucceed{errs: []error{errors.New("once")}}
	var failed []error
	_, err := DialRetry(context.Background(), tn, "1.2.3.4:5", RetryOptions{OnFail: func(err error) {
		failed = append(failed, err)
	}})
	var de *DialError
	if !errors.As(err, &de) || de.Attempts != 1 {
		t.Fatalf("default attempts = %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("OnFail runs = %d, want 1", len(failed))
	}
	if Retryable(Wrap(ClassHeartbeat, "", errors.New("dead"))) != true {
		t.Fatal("heartbeat class is retryable")
	}
}
