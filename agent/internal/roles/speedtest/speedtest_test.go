package speedtest

import (
	"context"

	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBaseURL(t *testing.T) {
	cases := map[string]string{
		"wss://panel.example.com/agent/ws": "https://panel.example.com",
		"ws://10.0.0.1:8080/agent/ws":      "http://10.0.0.1:8080",
		"https://panel.example.com/x":      "https://panel.example.com/x",
		"wss://h":                          "https://h",
	}
	for in, want := range cases {
		got, err := BaseURL(in)
		if err != nil {
			t.Fatalf("BaseURL(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("BaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := BaseURL("ftp://x"); err == nil {
		t.Fatal("unknown scheme must fail")
	}
}

func TestMeasure(t *testing.T) {
	const n = 256 << 10
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/speedtest/bytes" {
			http.NotFound(w, r)
			return
		}
		w.Write(make([]byte, n))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := Measure(ctx, srv.URL, n)
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != n {
		t.Fatalf("bytes = %d, want %d", res.Bytes, n)
	}
	if res.Seconds <= 0 || res.Mbps <= 0 {
		t.Fatalf("bad result: %+v", res)
	}

	// 服务端 500 即报错
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := Measure(ctx, bad.URL, n); err == nil {
		t.Fatal("server error must fail")
	}
}
