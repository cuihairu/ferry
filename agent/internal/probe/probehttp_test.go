package probe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestMeasureHTTPPhases covers the F-0942 web-session phases: a plaintext
// probe reports connect/ttfb but no TLS, a slow first byte lifts TTFB above
// the connect floor, and all phases are means over the successful rounds.
func TestMeasureHTTPPhases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond) // a visible server think-time
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	spec := config.ProbeSpec{Name: "http-phases", TargetKind: agentproto.ProbeTargetExit,
		Target: strings.TrimPrefix(srv.URL, "http://"), Proto: "http", IntervalSec: 5}
	_, _, _, reachable, _, phases := measureHTTP(spec)
	if !reachable {
		t.Fatal("probe must be reachable")
	}
	if phases.connectMs < 0 || phases.ttfbMs < 0 {
		t.Fatalf("phases = %+v; want non-negative means", phases)
	}
	if phases.ttfbMs < 20 {
		t.Fatalf("ttfb = %d ms; want the ~30ms think-time visible", phases.ttfbMs)
	}
	if phases.tlsMs != 0 {
		t.Fatalf("plaintext tls = %d; want 0", phases.tlsMs)
	}
	if phases.throughputKbps != 0 {
		t.Fatalf("HEAD probe throughput = %d; want 0 without download_kb", phases.throughputKbps)
	}
}

// TestMeasureHTTPThroughput covers the F-0943 opt-in: download_kb flips the
// round to GET, the body is read, and the report carries a nonzero rate that
// matches the served payload size and pacing.
func TestMeasureHTTPThroughput(t *testing.T) {
	const bodyKB = 256
	body := make([]byte, bodyKB*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("download probe must use GET, got %s", r.Method)
		}
		// pace the body so the rate has a real denominator
		w.Header().Set("Content-Length", "262144")
		if f, ok := w.(http.Flusher); ok {
			for chunk := 0; chunk < 8; chunk++ {
				_, _ = w.Write(body[chunk*32768 : (chunk+1)*32768])
				f.Flush()
				time.Sleep(20 * time.Millisecond)
			}
		} else {
			_, _ = w.Write(body)
		}
	}))
	defer srv.Close()
	spec := config.ProbeSpec{Name: "http-dl", TargetKind: agentproto.ProbeTargetExit,
		Target: strings.TrimPrefix(srv.URL, "http://"), Proto: "http",
		DownloadKB: bodyKB, IntervalSec: 5}
	_, _, loss, reachable, _, phases := measureHTTP(spec)
	if !reachable || loss != 0 {
		t.Fatalf("download probe must succeed, got loss=%d reachable=%v", loss, reachable)
	}
	// 256 KB in >=160ms => roughly 12.8 Mbit/s; a wide floor avoids flakiness
	// while still proving bytes were read and timed.
	if phases.throughputKbps < 1000 {
		t.Fatalf("throughput = %d kbps; want a real rate from the paced body", phases.throughputKbps)
	}
}

// TestValidateDownloadKB pins the agent-side knob guard (mirrored by the
// panel): only http/https probes may download, and the size is capped.
func TestValidateDownloadKB(t *testing.T) {
	base := config.ProbeSpec{Name: "dl", TargetKind: agentproto.ProbeTargetExit,
		Target: "example.com:443", Proto: "https", IntervalSec: 30}
	if err := config.ValidateProbeSpecs([]config.ProbeSpec{base}); err != nil {
		t.Fatalf("no download must pass: %v", err)
	}
	ok := base
	ok.DownloadKB = 10240
	if err := config.ValidateProbeSpecs([]config.ProbeSpec{ok}); err != nil {
		t.Fatalf("10240 must pass: %v", err)
	}
	big := base
	big.DownloadKB = 10241
	if err := config.ValidateProbeSpecs([]config.ProbeSpec{big}); err == nil || !strings.Contains(err.Error(), "download_kb") {
		t.Fatalf("10241 must fail with download_kb: %v", err)
	}
	zero := base
	zero.DownloadKB = -1
	if err := config.ValidateProbeSpecs([]config.ProbeSpec{zero}); err == nil {
		t.Fatal("negative download_kb must fail")
	}
}
