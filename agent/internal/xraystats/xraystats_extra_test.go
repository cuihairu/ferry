package xraystats

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// startH2C starts an h2c plaintext service (grpcUnary connects directly with AllowHTTP).
func startH2C(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGrpcUnaryNewRequestError covers an invalid address: building the request fails and errors.
func TestGrpcUnaryNewRequestError(t *testing.T) {
	if _, err := grpcUnary(context.Background(), "bad\naddr", "/xray.app.StatsService/QueryStats", nil); err == nil {
		t.Fatal("want error on invalid addr")
	}
}

// TestGrpcUnaryHTTPStatus covers non-application errors: an HTTP status other than 200 must error.
func TestGrpcUnaryHTTPStatus(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc http status 404") {
		t.Fatalf("err = %v; want grpc http status 404", err)
	}
}

// TestGrpcUnaryShortBody covers a body shorter than the 5-byte framing header: it reports body too short.
func TestGrpcUnaryShortBody(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc"))
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc body too short") {
		t.Fatalf("err = %v; want grpc body too short", err)
	}
}

// TestGrpcUnaryTruncatedBody covers the framing header declaring more than actually arrives: it reports truncated.
func TestGrpcUnaryTruncatedBody(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		body := make([]byte, 5)
		binary.BigEndian.PutUint32(body[1:5], 100) // declares 100, delivers 0
		_, _ = w.Write(body)
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc body truncated") {
		t.Fatalf("err = %v; want grpc body truncated", err)
	}
}

// TestPBConsumeErrors covers malformed input to the hand-written proto codec: tag and varint each error.
func TestPBConsumeErrors(t *testing.T) {
	if _, _, _, err := pbConsumeTag(nil); err == nil {
		t.Fatal("pbConsumeTag(nil) should error")
	}
	if _, _, err := pbConsumeVarint([]byte{0xFF}); err == nil {
		t.Fatal("pbConsumeVarint(truncated) should error")
	}
}

// TestPBSkipBadLength covers pbSkip erroring on a malformed wire2 length field.
func TestPBSkipBadLength(t *testing.T) {
	if _, err := pbSkip([]byte{0xFF}, 2); err == nil {
		t.Fatal("pbSkip wire2 with bad varint should error")
	}
}

// TestDecodeStatMalformed covers decodeStat's malformed branches: a wrong tag, a wrong name length,
// a wrong value varint, and a skip error on an unknown field.
func TestDecodeStatMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"bad tag", []byte{0xFF}},
		{"name len varint bad", []byte{0x0A, 0xFF}},
		{"name len exceeds", []byte{0x0A, 0x10, 'a'}},
		{"value varint bad", []byte{0x10, 0xFF}},
		{"unknown field skip fail", []byte{0x1A, 0xFF}}, // field3 wire2 + a bad length
	}
	for _, tc := range cases {
		if _, err := decodeStat(tc.in); err == nil {
			t.Errorf("%s: decodeStat(% x) should error", tc.name, tc.in)
		}
	}
}

// TestDecodeQueryStatsRespMalformed covers decodeQueryStatsResp's malformed branches.
func TestDecodeQueryStatsRespMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"bad tag", []byte{0xFF}},
		{"stat len varint bad", []byte{0x0A, 0xFF}},
		{"stat len exceeds", []byte{0x0A, 0x10, 'a'}},
		{"stat body malformed", []byte{0x0A, 0x01, 0xFF}},
		{"unknown field skip fail", []byte{0x12, 0xFF}}, // field2 wire2 + a bad length
	}
	for _, tc := range cases {
		if _, err := decodeQueryStatsResp(tc.in); err == nil {
			t.Errorf("%s: decodeQueryStatsResp(% x) should error", tc.name, tc.in)
		}
	}
}

// TestGrpcUnescapePlain covers no percent sign: returned as is (the fast path).
func TestGrpcUnescapePlain(t *testing.T) {
	if got := grpcUnescape("plain message"); got != "plain message" {
		t.Fatalf("got %q", got)
	}
}
