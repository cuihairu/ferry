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

	"github.com/cuihairu/ferry/packages/agentproto"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// errShortProto is the truncation sentinel used when the test parses a request.
var errShortProto = errors.New("bad proto length")

// capturedReq records the key request parameters the fake service received.
type capturedReq struct {
	pattern string
	reset   bool
}

// startFakeStats starts an h2c fake StatsService: QueryStats returns a fixed result,
// and records the requests it received for assertions (whether pattern/reset were passed as promised).
// The transport matches a real machine: gRPC over h2c, with the reply carrying gRPC framing and a grpc-status trailer.
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
		// strip the gRPC framing from the request body and parse the Request, checking the client's encoding
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

		// the reply: content-type + a declared trailer, the body being the framed Response
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

// decodeQueryStatsReq parses QueryStatsRequest{1:pattern, 2:reset} (used by test assertions).
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

// encodeQueryStatsResp encodes QueryStatsResponse{1: repeated Stat} (used by the fake service).
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

// TestQueryUserStats covers the main path: the request is encoded as promised (pattern/reset)
// and the reply decodes correctly into per-email up/down.
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

// TestQueryUserStatsGrpcError covers the agent error path: a non-zero grpc-status
// (e.g. the xray api not being enabled) must propagate upward with the error (including percent-escape decoding) instead of an empty table.
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

// TestQueryUserStatsConnRefused covers a failed connection: the query must error instead of silently returning an empty table.
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

// TestParseUserStats covers counter-name parsing: user>>> aggregated, everything else ignored, same names accumulated.
func TestParseUserStats(t *testing.T) {
	got := ParseUserStats([]Stat{
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 1},
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 2}, // same-name entries accumulate
		{Name: "user>>>a@ferry>>>traffic>>>downlink", Value: 10},
		{Name: "inbound>>>in-1>>>traffic>>>uplink", Value: 99}, // non-user entries ignored
		{Name: "user>>>b@ferry>>>traffic>>>unknown", Value: 7}, // unknown directions ignored
		{Name: "user>>>b@ferry>>>something", Value: 8},         // wrong segment counts ignored
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

// TestQueryOutboundStats covers the routed outbound counter query (SAVE-4/7): the request uses
// pattern=outbound>>> with reset=true, taking the direct and block deltas in the same query.
func TestQueryOutboundStats(t *testing.T) {
	resp := []Stat{
		{Name: "outbound>>>block>>>traffic>>>uplink", Value: 123},
		{Name: "outbound>>>block>>>traffic>>>downlink", Value: 4},
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 999},
		{Name: "outbound>>>direct>>>traffic>>>downlink", Value: 1001},
	}
	srv, seen := startFakeStats(t, resp)
	c := New(strings.TrimPrefix(srv.URL, "http://"), log.New(io.Discard, "", 0))

	got, err := c.QueryOutboundStats(context.Background())
	if err != nil {
		t.Fatalf("QueryOutboundStats: %v", err)
	}
	if seen.pattern != "outbound>>>" || !seen.reset {
		t.Fatalf("request = %+v, want pattern=outbound>>> reset=true", seen)
	}
	if got.Blocked != 127 {
		t.Fatalf("blocked = %d, want 127 (uplink+downlink)", got.Blocked)
	}
	if got.Direct != 2000 {
		t.Fatalf("direct = %d, want 2000", got.Direct)
	}
}

// TestParseOutboundStats covers routed outbound counter parsing: only the traffic counters of the
// direct/block outbounds count (up and down summed separately); other outbounds/inbounds/near-miss names/wrong segment counts are ignored.
func TestParseOutboundStats(t *testing.T) {
	got := ParseOutboundStats([]Stat{
		{Name: "outbound>>>block>>>traffic>>>uplink", Value: 10},
		{Name: "outbound>>>block>>>traffic>>>uplink", Value: 5}, // same names accumulate
		{Name: "outbound>>>block>>>traffic>>>downlink", Value: 2},
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 100},
		{Name: "outbound>>>direct>>>traffic>>>downlink", Value: 30},
		{Name: "inbound>>>in-1>>>traffic>>>uplink", Value: 50},     // inbounds ignored
		{Name: "outbound>>>blocked>>>traffic>>>uplink", Value: 30}, // near-miss names are never taken in by mistake
		{Name: "outbound>>>block>>>something", Value: 7},           // wrong segment counts ignored
	})
	if got.Blocked != 17 {
		t.Fatalf("blocked = %d, want 17", got.Blocked)
	}
	if got.Direct != 130 {
		t.Fatalf("direct = %d, want 130", got.Direct)
	}
}

// TestCollectorQueries covers the Collector's three rules: the node-level delta is the sum of user deltas
// with conns always 0, per-email zero deltas are never reported, and outbounds are queried separately per pattern.
func TestCollectorQueries(t *testing.T) {
	srv, seen := startFakeStats(t, []Stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 10},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 40},
		{Name: "user>>>bob>>>traffic>>>downlink", Value: 2},
		{Name: "user>>>idle>>>traffic>>>downlink", Value: 0}, // a zero delta is never reported
	})
	addr := strings.TrimPrefix(srv.URL, "http://")
	c := NewCollector(addr, log.New(io.Discard, "", 0))

	rx, tx, conns, err := c.Collect("xray")
	if err != nil {
		t.Fatalf("Collect = %v", err)
	}
	if rx != 42 || tx != 10 || conns != 0 {
		t.Fatalf("Collect = %d, %d, %d; want 42, 10, 0", rx, tx, conns)
	}
	if seen.pattern != "user>>>" || !seen.reset {
		t.Fatalf("query = %+v; want user>>> reset", *seen)
	}

	users, err := c.CollectUsers("xray")
	if err != nil {
		t.Fatalf("CollectUsers = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %+v; want 2 (zero delta dropped)", users)
	}
	byEmail := map[string]agentproto.UserTraffic{}
	for _, u := range users {
		byEmail[u.Email] = u
	}
	if a := byEmail["alice"]; a.Rx != 40 || a.Tx != 10 {
		t.Fatalf("alice = %+v; want rx 40 tx 10", a)
	}
}

// TestCollectorOutbound covers outbound collection using the outbound>>> rule.
func TestCollectorOutbound(t *testing.T) {
	srv, seen := startFakeStats(t, []Stat{
		{Name: "outbound>>>direct>>>traffic>>>uplink", Value: 100},
		{Name: "outbound>>>block>>>traffic>>>uplink", Value: 7},
	})
	c := NewCollector(strings.TrimPrefix(srv.URL, "http://"), nil) // a nil logger uses the default
	direct, blocked, err := c.CollectOutbound("xray")
	if err != nil {
		t.Fatalf("CollectOutbound = %v", err)
	}
	if direct != 100 || blocked != 7 {
		t.Fatalf("direct, blocked = %d, %d; want 100, 7", direct, blocked)
	}
	if seen.pattern != "outbound>>>" {
		t.Fatalf("pattern = %q; want outbound>>>", seen.pattern)
	}
}

// TestCollectorQueryError covers all three query-failure rules erroring individually, never swallowing an error.
func TestCollectorQueryError(t *testing.T) {
	c := NewCollector("127.0.0.1:1", log.New(io.Discard, "", 0))
	if _, _, _, err := c.Collect("xray"); err == nil {
		t.Fatal("Collect on dead addr should error")
	}
	if _, err := c.CollectUsers("xray"); err == nil {
		t.Fatal("CollectUsers on dead addr should error")
	}
	if _, _, err := c.CollectOutbound("xray"); err == nil {
		t.Fatal("CollectOutbound on dead addr should error")
	}
}

// TestPBSkip covers skipping the bodies of fields we do not care about: varint/fixed64/length-delimited/fixed32,
// all four wire types are skippable, while a length out of range and an unknown wire type error.
func TestPBSkip(t *testing.T) {
	rest := []byte("tail")
	cases := []struct {
		name string
		body []byte
		wire uint64
		want string
	}{
		{"varint", binary.AppendUvarint(nil, 300), 0, "tail"},
		{"fixed64", make([]byte, 8), 1, "tail"},
		{"bytes", append(binary.AppendUvarint(nil, 3), "abc"...), 2, "tail"},
		{"fixed32", make([]byte, 4), 5, "tail"},
	}
	for _, c := range cases {
		b := append(append([]byte(nil), c.body...), rest...)
		got, err := pbSkip(b, c.wire)
		if err != nil || string(got) != c.want {
			t.Errorf("pbSkip %s = %q, %v; want %q, nil", c.name, got, err, c.want)
		}
	}
	if _, err := pbSkip(make([]byte, 2), 1); err == nil {
		t.Error("short fixed64 should error")
	}
	if _, err := pbSkip([]byte{9}, 2); err == nil {
		t.Error("length beyond body should error")
	}
	if _, err := pbSkip(make([]byte, 1), 5); err == nil {
		t.Error("short fixed32 should error")
	}
	if _, err := pbSkip(nil, 3); err == nil {
		t.Error("group wire type should error")
	}
}
