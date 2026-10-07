// Package speedtest 是自动测速校准（E-8）：注册后从面板下载固定字节，
// 算出实测下行容量。轻量（默认 4MB，一次），偏差大以实测为准的判定在面板。
package speedtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBytes 是单次测速字节数（与面板 speedtestDefault 对齐）。
const DefaultBytes = 4 << 20

// Timeout 是单次测速总超时（含建连与下载）。
const Timeout = 60 * time.Second

// BaseURL 从 agent 的 PanelURL 派生面板 http(s) 根地址：
// wss://host/agent/ws → https://host；ws:// → http://。
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

// Result 是单次测速结果。
type Result struct {
	Bytes   int64
	Seconds float64
	// Mbps 是实测下行容量（兆比特/秒）。
	Mbps int
}

// Measure 下载 n 字节并测速（n<=0 取默认）。
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
