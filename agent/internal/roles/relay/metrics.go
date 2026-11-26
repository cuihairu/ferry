package relay

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

// relayMetrics counters (F-0815): the relay's own connection/dial activity, exposed
// through a local HTTP endpoint so the agent core's process-metrics collector
// (procs.MetricsURL) can report them with every heartbeat.
type relayMetrics struct {
	conns        atomic.Int64 // accepted frontend connections
	dropped      atomic.Int64 // refused by the connection ceiling
	streamDrops  atomic.Int64 // dropped by the stream ceiling
	dials        atomic.Int64 // landing dial attempts
	dialFailures atomic.Int64 // landing dial failures after retries
	bytesRx      atomic.Int64 // bytes copied frontend->landing
	bytesTx      atomic.Int64 // bytes copied landing->frontend
}

// snapshot renders the counters in the parser-compatible format: JSON is tried
// first by the poller, and every key also appears as a whitespace line so the
// key-value fallback works too.
func (m *relayMetrics) snapshot() map[string]uint64 {
	return map[string]uint64{
		"relay_conns":         uint64(m.conns.Load()),
		"relay_dropped":       uint64(m.dropped.Load()),
		"relay_stream_drops":  uint64(m.streamDrops.Load()),
		"relay_dials":         uint64(m.dials.Load()),
		"relay_dial_failures": uint64(m.dialFailures.Load()),
		"relay_bytes_rx":      uint64(m.bytesRx.Load()),
		"relay_bytes_tx":      uint64(m.bytesTx.Load()),
	}
}

// serveMetrics starts the local metrics endpoint; a blank listen address keeps
// the endpoint disabled (F-0815).
func serveMetrics(addr string, m *relayMetrics) {
	if addr == "" {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return // a busy metrics port must not stop the relay
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		var sb strings.Builder
		for _, k := range []string{
			"relay_conns", "relay_dropped", "relay_stream_drops", "relay_dials",
			"relay_dial_failures", "relay_bytes_rx", "relay_bytes_tx",
		} {
			fmt.Fprintf(&sb, "%s %d\n", k, m.snapshot()[k])
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(sb.String()))
	})
	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(ln)
	}()
}
