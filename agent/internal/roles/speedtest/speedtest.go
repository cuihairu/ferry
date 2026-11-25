// Package speedtest is the automatic speed-test calibration (E-8): after registration it downloads a fixed number of bytes from the panel
// and computes the measured download capacity. It is lightweight (4MB by default, once); deciding that a large deviation means the measured value wins lives in the panel.
package speedtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBytes is the number of bytes per speed test (aligned with the panel's speedtestDefault).
const DefaultBytes = 4 << 20

// Timeout is the total timeout of one speed test (connecting and downloading included).
const Timeout = 60 * time.Second

// BaseURL derives the panel's http(s) root address from the agent's PanelURL:
// wss://host/agent/ws -> https://host; ws:// -> http://.
func BaseURL(panelURL string) (string, error) {
	u := panelURL
	switch {
	case strings.HasPrefix(u, "wss://"):
		u = "https://" + strings.TrimPrefix(u, "wss://")
	case strings.HasPrefix(u, "ws://"):
		u = "http://" + strings.TrimPrefix(u, "ws://")
	case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "http://"):
	default:
		return "", fmt.Errorf("speedtest: unknown panel URL scheme %q", panelURL)
	}
	if i := strings.Index(u, "/agent/ws"); i >= 0 {
		u = u[:i]
	}
	return strings.TrimSuffix(u, "/"), nil
}

// Result is the outcome of one speed test.
type Result struct {
	Bytes   int64
	Seconds float64
	// Mbps is the measured download capacity (megabits per second).
	Mbps int
}

// Measure downloads n bytes and measures the speed (n<=0 uses the default).
func Measure(ctx context.Context, base string, n int64) (Result, error) {
	if n <= 0 {
		n = DefaultBytes
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/speedtest/bytes?n=%d", base, n), nil)
	if err != nil {
		return Result{}, err
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("speedtest: status %d", resp.StatusCode)
	}
	got, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return Result{}, err
	}
	secs := time.Since(start).Seconds()
	if secs <= 0 || got == 0 {
		return Result{}, fmt.Errorf("speedtest: no data")
	}
	return Result{
		Bytes:   got,
		Seconds: secs,
		Mbps:    int(float64(got) * 8 / 1e6 / secs),
	}, nil
}
