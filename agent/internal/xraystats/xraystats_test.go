package xraystats

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// errShortProto 是测试解请求时的截断哨兵。
var errShortProto = errors.New("bad proto length")

// capturedReq 记录假服务收到的请求关键参数。
type capturedReq struct {
	pattern string
	reset   bool
}

// startFakeStats 起一个 h2c 假 StatsService：QueryStats 返回固定结果，
// 并记录收到的请求供断言（pattern/reset 是否按约传递）。
// 传输口径与真机一致：gRPC over h2c，应答带 gRPC 分帧与 grpc-status trailer。
func startFakeStats(t *testing.T, resp []Stat) (*httptest.Server, *capturedReq) {
	t.Helper()
	seen := &capturedReq{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != queryStatsMethod {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// 请求体剥 gRPC 分帧后解 Request，验证客户端编码
		if len(body) < 5 {
			http.Error(w, "short body", http.StatusBadRequest)
			return
		}
		req, perr := decodeQueryStatsReq(body[5:])
		if perr != nil {
			http.Error(w, perr.Error(), http.StatusBadRequest)
			return
		}
		seen.pattern, seen.reset = req.pattern, req.reset

		// 应答：content-type + 声明 trailer，body 为分帧后的 Response
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Trailer", "Grpc-Status")
		payload := encodeQueryStatsResp(resp)
		frame := make([]byte, 5+len(payload))
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
		copy(frame[5:], payload)
		w.Header().Set("Grpc-Status", "0")
		_, _ = w.Write(frame)
	})
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// decodeQueryStatsReq 解 QueryStatsRequest{1:pattern, 2:reset}（测试断言用）。
func decodeQueryStatsReq(b []byte) (capturedReq, error) {
	var out capturedReq
	for len(b) > 0 {
		field, wire, rest, err := pbConsumeTag(b)
		if err != nil {
			return out, err
		}
		switch {
		case field == 1 && wire == 2:
			l, r, err := pbConsumeVarint(rest)
			if err != nil {
				return out, err
			}
			if uint64(len(r)) < l {
				return out, errShortProto
			}
			out.pattern = string(r[:l])
			b = r[l:]
		case field == 2 && wire == 0:
			v, r, err := pbConsumeVarint(rest)
			if err != nil {
				return out, err
			}
			out.reset = v != 0
			b = r
		default:
			if b, err = pbSkip(rest, wire); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// encodeQueryStatsResp 编 QueryStatsResponse{1: repeated Stat}（假服务用）。
func encodeQueryStatsResp(stats []Stat) []byte {
	var b []byte
	for _, s := range stats {
		var body []byte
		body = pbAppendBytes(body, 1, []byte(s.Name))
		body = pbAppendVarint(body, 2, s.Value)
		b = pbAppendBytes(b, 1, body)
	}
	return b
}

// TestQueryUserStats 覆盖主通路：请求按约编码（pattern/reset）、
// 应答正确解为 per-email 上下行。
func TestQueryUserStats(t *testing.T) {
	resp := []Stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 120},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 340},
		{Name: "user>>>bob>>>traffic>>>downlink", Value: 5},
	}
	srv, seen := startFakeStats(t, resp)
	c := New(strings.TrimPrefix(srv.URL, "http://"), log.New(io.Discard, "", 0))

	got, err := c.QueryUserStats(context.Background())
	if err != nil {
		t.Fatalf("QueryUserStats: %v", err)
	}
	if seen.pattern != "user>>>" || !seen.reset {
		t.Fatalf("request = %+v, want pattern=user>>> reset=true", seen)
	}
	alice := got["alice"]
	if alice.Tx != 120 || alice.Rx != 340 {
		t.Fatalf("alice = %+v, want tx=120 rx=340", alice)
	}
	if bob := got["bob"]; bob.Rx != 5 || bob.Tx != 0 {
		t.Fatalf("bob = %+v, want rx=5", bob)
	}
}

// TestQueryUserStatsGrpcError 覆盖 agent 报错路径：grpc-status 非 0
// （如 xray api 未开）要向上传错（含 percent-escape 解码）而不是空表。
func TestQueryUserStatsGrpcError(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.Header().Set("Grpc-Status", "2")
		w.Header().Set("Grpc-Message", "unknown%20service")
		_, _ = w.Write(nil)
	})
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	defer srv.Close()

	c := New(strings.TrimPrefix(srv.URL, "http://"), log.New(io.Discard, "", 0))
	_, err := c.QueryUserStats(context.Background())
	if err == nil {
		t.Fatal("want error on grpc status 2")
	}
	if !strings.Contains(err.Error(), "grpc status 2") || !strings.Contains(err.Error(), "unknown service") {
		t.Fatalf("err = %v, want grpc status 2 + 解 escape 消息", err)
	}
}

// TestQueryUserStatsConnRefused 覆盖连接失败：查询要报错而不是静默空表。
func TestQueryUserStatsConnRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c := New(addr, log.New(io.Discard, "", 0))
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := c.QueryUserStats(ctx); err == nil {
		t.Fatal("want error on closed api")
	}
}

// TestParseUserStats 覆盖计数器名解析：user>>> 聚合、其余忽略、同名累加。
func TestParseUserStats(t *testing.T) {
	got := ParseUserStats([]Stat{
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 1},
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 2}, // 同名条目累加
		{Name: "user>>>a@ferry>>>traffic>>>downlink", Value: 10},
		{Name: "inbound>>>in-1>>>traffic>>>uplink", Value: 99}, // 非 user 条目忽略
		{Name: "user>>>b@ferry>>>traffic>>>unknown", Value: 7}, // 未知方向忽略
		{Name: "user>>>b@ferry>>>something", Value: 8},         // 段数不符忽略
	})
	if len(got) != 1 {
		t.Fatalf("users = %d, want 1 (b@ferry 条目全部无效): %+v", len(got), got)
	}
	if u := got["a@ferry"]; u.Tx != 3 || u.Rx != 10 {
		t.Fatalf("a@ferry = %+v, want tx=3 rx=10", u)
	}
	if u, ok := got["b@ferry"]; ok {
		t.Fatalf("b@ferry 不应有无增量条目, got %+v", u)
	}
}
