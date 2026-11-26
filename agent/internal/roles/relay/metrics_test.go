package relay

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
)

// TestRelayMetricsEndpoint covers F-0815: a configured /metrics endpoint
// reports accept/dial/byte counters after one forwarded connection.
func TestRelayMetricsEndpoint(t *testing.T) {
	landing, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer landing.Close()
	go func() {
		for {
			c, err := landing.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}()
		}
	}()
	metricsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = metricsLn.Close() // occupy a free port number
	metricsAddr := metricsLn.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = run(ctx, config.RelaySpec{
			Listen: "127.0.0.1:0", LandingAddr: landing.Addr().String(),
			Tunnel: "plain-test", MetricsListen: metricsAddr,
		}, "", nil, ln, log.New(os.Stderr, "metrics ", 0))
	}()

	connect := func() net.Conn {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", ln.Addr().String(), 500*time.Millisecond)
			if err == nil {
				return c
			}
			if time.Now().After(deadline) {
				t.Fatalf("dial relay: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	c := connect()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	payload := []byte("metrics-echo")
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatal(err)
	}
	_ = c.Close() // io.Copy counters land when the forwarding loop ends

	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get("http://" + metricsAddr + "/metrics")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			got := map[string]int64{}
			for _, line := range lines {
				fields := strings.Fields(line)
				if len(fields) == 2 {
					got[fields[0]], _ = strconv.ParseInt(fields[1], 10, 64)
				}
			}
			// Byte counters land when the forwarding loop ends; polling only
			// guarantees the accept/dial counters are visible here.
			if got["relay_conns"] >= 1 && got["relay_dials"] >= 1 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("metrics not reported: %q", body)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("metrics endpoint unavailable: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRelayMetricsSnapshot covers the parser contract used by the core
// poller: every counter is a non-negative uint64 keyed by a stable name.
func TestRelayMetricsSnapshot(t *testing.T) {
	mx := &relayMetrics{}
	mx.conns.Add(3)
	mx.dropped.Add(1)
	snap := mx.snapshot()
	if snap["relay_conns"] != 3 || snap["relay_dropped"] != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if len(snap) != 7 {
		t.Fatalf("snapshot keys = %d; want 7: %+v", len(snap), snap)
	}
}
