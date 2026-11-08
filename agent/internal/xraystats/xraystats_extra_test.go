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

// startH2C 起一个 h2c 明文服务（grpcUnary 走 AllowHTTP 直连）。
func startH2C(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGrpcUnaryNewRequestError 非法地址：请求构造失败即报错。
func TestGrpcUnaryNewRequestError(t *testing.T) {
	if _, err := grpcUnary(context.Background(), "bad\naddr", "/xray.app.StatsService/QueryStats", nil); err == nil {
		t.Fatal("want error on invalid addr")
	}
}

// TestGrpcUnaryHTTPStatus 非应用层错误：HTTP 状态非 200 要报错。
func TestGrpcUnaryHTTPStatus(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc http status 404") {
		t.Fatalf("err = %v; want grpc http status 404", err)
	}
}

// TestGrpcUnaryShortBody 应答体不足 5 字节分帧头：报 body too short。
func TestGrpcUnaryShortBody(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc"))
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc body too short") {
		t.Fatalf("err = %v; want grpc body too short", err)
	}
}

// TestGrpcUnaryTruncatedBody 分帧头声明长度超出实际：报 truncated。
func TestGrpcUnaryTruncatedBody(t *testing.T) {
	srv := startH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		body := make([]byte, 5)
		binary.BigEndian.PutUint32(body[1:5], 100) // 声明 100，实给 0
		_, _ = w.Write(body)
	})
	_, err := grpcUnary(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "/q", nil)
	if err == nil || !strings.Contains(err.Error(), "grpc body truncated") {
		t.Fatalf("err = %v; want grpc body truncated", err)
	}
}

// TestPBConsumeErrors 手写 proto 编解码的畸形输入：tag 与 varint 各自报错。
func TestPBConsumeErrors(t *testing.T) {
	if _, _, _, err := pbConsumeTag(nil); err == nil {
		t.Fatal("pbConsumeTag(nil) should error")
	}
	if _, _, err := pbConsumeVarint([]byte{0xFF}); err == nil {
		t.Fatal("pbConsumeVarint(truncated) should error")
	}
}

// TestPBSkipBadLength pbSkip 对 wire2 的长度字段畸形要报错。
func TestPBSkipBadLength(t *testing.T) {
	if _, err := pbSkip([]byte{0xFF}, 2); err == nil {
		t.Fatal("pbSkip wire2 with bad varint should error")
	}
}

// TestDecodeStatMalformed decodeStat 的各畸形分支：tag 错、名字长度错、
// 值 varint 错、未知字段 skip 错。
func TestDecodeStatMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"bad tag", []byte{0xFF}},
		{"name len varint bad", []byte{0x0A, 0xFF}},
		{"name len exceeds", []byte{0x0A, 0x10, 'a'}},
		{"value varint bad", []byte{0x10, 0xFF}},
		{"unknown field skip fail", []byte{0x1A, 0xFF}}, // field3 wire2 + 坏长度
	}
	for _, tc := range cases {
		if _, err := decodeStat(tc.in); err == nil {
			t.Errorf("%s: decodeStat(% x) should error", tc.name, tc.in)
		}
	}
}

// TestDecodeQueryStatsRespMalformed decodeQueryStatsResp 的各畸形分支。
func TestDecodeQueryStatsRespMalformed(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"bad tag", []byte{0xFF}},
		{"stat len varint bad", []byte{0x0A, 0xFF}},
		{"stat len exceeds", []byte{0x0A, 0x10, 'a'}},
		{"stat body malformed", []byte{0x0A, 0x01, 0xFF}},
		{"unknown field skip fail", []byte{0x12, 0xFF}}, // field2 wire2 + 坏长度
	}
	for _, tc := range cases {
		if _, err := decodeQueryStatsResp(tc.in); err == nil {
			t.Errorf("%s: decodeQueryStatsResp(% x) should error", tc.name, tc.in)
		}
	}
}

// TestGrpcUnescapePlain 无百分号时原样返回（快路径）。
func TestGrpcUnescapePlain(t *testing.T) {
	if got := grpcUnescape("plain message"); got != "plain message" {
		t.Fatalf("got %q", got)
	}
}
