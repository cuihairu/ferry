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

// TestNewDefaultsLogger logger 缺省：用默认 logger 且构造可用。
func TestNewDefaultsLogger(t *testing.T) {
	r := New(nil, nil)
	if r == nil || r.log == nil {
		t.Fatal("New must default the logger")
	}
}

// TestProbeOnceWithoutSend 离线（未设发送口）：结论静默丢弃不报错。
func TestProbeOnceWithoutSend(t *testing.T) {
	r := New([]config.ProbeSpec{{Name: "quiet"}}, log.New(io.Discard, "", 0))
	r.probeOnce(config.ProbeSpec{Name: "quiet", Target: "127.0.0.1:1"}) // send == nil 分支
}

// TestProbeOnceSendFailure 上报失败：记日志不重试不 panic。
func TestProbeOnceSendFailure(t *testing.T) {
	var buf bytes.Buffer
	r := New(nil, log.New(&buf, "", 0))
	r.SetSend(func(agentproto.Envelope) error { return errors.New("link down") })
	r.probeOnce(config.ProbeSpec{Name: "flaky", Target: "127.0.0.1:1"})
	if !bytes.Contains(buf.Bytes(), []byte("probe flaky report")) {
		t.Fatalf("send failure not logged: %q", buf.String())
	}
}

// TestNextIntervalDefaultsZero interval 未配（<=0）：回落 30s 缺省。
func TestNextIntervalDefaultsZero(t *testing.T) {
	spec := config.ProbeSpec{Direction: agentproto.DirectionOut, IntervalSec: 0}
	if got := nextInterval(spec, time.Now()); got != 30*time.Second {
		t.Fatalf("nextInterval = %v; want 30s default", got)
	}
}
