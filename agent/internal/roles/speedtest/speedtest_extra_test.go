package speedtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMeasureDefaultBytes covers n<=0 taking the default byte count (aligned with the panel's speedtestDefault).
func TestMeasureDefaultBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, DefaultBytes))
	}))
	defer srv.Close()
	res, err := Measure(context.Background(), srv.URL, 0)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if res.Bytes != DefaultBytes {
		t.Fatalf("bytes = %d; want default %d", res.Bytes, DefaultBytes)
	}
	if res.Mbps <= 0 {
		t.Fatalf("mbps = %d; want positive", res.Mbps)
	}
}

// TestMeasureBadBaseURL covers an invalid panel root address: building the request fails right away.
func TestMeasureBadBaseURL(t *testing.T) {
	if _, err := Measure(context.Background(), "http://bad\nurl.example", 100); err == nil {
		t.Fatal("malformed base URL must error")
	}
}

// TestMeasureConnRefused covers an unreachable panel: an error is reported.
func TestMeasureConnRefused(t *testing.T) {
	if _, err := Measure(context.Background(), "http://127.0.0.1:1", 100); err == nil {
		t.Fatal("refused endpoint must error")
	}
}

// TestMeasureTruncatedBody covers a declared length not matching reality: the download is cut short and errors.
func TestMeasureTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short")) // closes after sending only part of it
	}))
	defer srv.Close()
	if _, err := Measure(context.Background(), srv.URL, 1000); err == nil {
		t.Fatal("truncated body must error")
	}
}

// TestMeasureNoData covers an empty response body: judged an invalid result rather than a division by zero / 0 Mbps.
func TestMeasureNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := Measure(context.Background(), srv.URL, 100)
	if err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("err = %v; want no data error", err)
	}
}
