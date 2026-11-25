package probe

import (
	"bytes"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestNewDefaultsLogger covers a nil logger: the default logger is used and the constructor stays usable.
func TestNewDefaultsLogger(t *testing.T) {
	r := New(nil, nil)
	if r == nil || r.log == nil {
		t.Fatal("New must default the logger")
	}
}

// TestProbeOnceWithoutSend covers being offline (no sender set): the conclusion is dropped silently without an error.
func TestProbeOnceWithoutSend(t *testing.T) {
	r := New([]config.ProbeSpec{{Name: "quiet"}}, log.New(io.Discard, "", 0))
	r.probeOnce(config.ProbeSpec{Name: "quiet", Target: "127.0.0.1:1"}) // the send == nil branch
}

// TestProbeOnceSendFailure covers a failed report: log it, no retry, no panic.
func TestProbeOnceSendFailure(t *testing.T) {
	var buf bytes.Buffer
	r := New(nil, log.New(&buf, "", 0))
	r.SetSend(func(agentproto.Envelope) error { return errors.New("link down") })
	r.probeOnce(config.ProbeSpec{Name: "flaky", Target: "127.0.0.1:1"})
	if !bytes.Contains(buf.Bytes(), []byte("probe flaky report")) {
		t.Fatalf("send failure not logged: %q", buf.String())
	}
}

// TestNextIntervalDefaultsZero covers an unset interval (<=0): fall back to the 30s default.
func TestNextIntervalDefaultsZero(t *testing.T) {
	spec := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 0}
	if got := nextInterval(spec, time.Now()); got != 30*time.Second {
		t.Fatalf("nextInterval = %v; want 30s default", got)
	}
}
