package traffic

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestDispatchUnknownProc covers the dispatcher erroring on an unregistered process and delegating normally for registered ones.
func TestDispatchUnknownProc(t *testing.T) {
	d := dispatchCollector{"xray": fakeCollector{}}
	if _, _, _, err := d.Collect("ghost"); err == nil {
		t.Fatal("unknown proc should error")
	}
	rx, tx, conns, err := d.Collect("xray")
	if err != nil || rx != 100 || tx != 200 || conns != 3 {
		t.Fatalf("delegate = %d/%d/%d, err %v; want 100/200/3 nil", rx, tx, conns, err)
	}
}

// TestNewNilLogger covers a nil logger: no panic and the default logger is used (construction only).
func TestNewNilLogger(t *testing.T) {
	r := New(nil, time.Second, nil, nil)
	if r == nil || r.log == nil {
		t.Fatal("New must tolerate nil logger")
	}
}

// TestRunZeroIntervalReturns covers interval<=0 disabling periodic collection: Run returns immediately.
func TestRunZeroIntervalReturns(t *testing.T) {
	r := New(nil, 0, fakeCollector{}, log.New(io.Discard, "", 0))
	done := make(chan struct{})
	go func() {
		r.Run(context.Background()) // the ctx is never cancelled; Run exits by itself via interval<=0
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run with interval<=0 must return immediately")
	}
}

// TestReportSendFailure covers a failed report send: log it without panicking, leaving the data on the collection side.
func TestReportSendFailure(t *testing.T) {
	r := New(specs(), 10*time.Millisecond, fakeCollector{}, log.New(io.Discard, "", 0))
	r.SetSend(func(agentproto.Envelope) error { return errors.New("link down") })
	r.report(r.collectAll()) // must not panic; the de-duplicated logging of same-kind errors is collectErr's job
}
