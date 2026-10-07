package xraystats

import (
	"context"
	"io"
	"log"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

// capturedReq 记录假服务收到的请求关键参数（proto 消息不可整拷，只留字段）。
type capturedReq struct {
	pattern string
	reset   bool
}

// startFakeStats 起一个假 StatsService：QueryStats 返回固定结果，
// 并记录收到的请求供断言（pattern/reset 是否按约传递）。
func startFakeStats(t *testing.T, resp *QueryStatsResponse) (*bufconn.Listener, *capturedReq) {
	t.Helper()
	ln := bufconn.Listen(1024 * 1024)
	seen := &capturedReq{}
	srv := grpc.NewServer()
	desc := grpc.ServiceDesc{
		ServiceName: "xray.app.stats.command.StatsService",
		Methods: []grpc.MethodDesc{{
			MethodName: "QueryStats",
			Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, icpt grpc.UnaryServerInterceptor) (interface{}, error) {
				in := new(QueryStatsRequest)
				if err := dec(in); err != nil {
					return nil, err
				}
				seen.pattern, seen.reset = in.Pattern, in.Reset_
				if icpt == nil {
					return resp, nil
				}
				info := &grpc.UnaryServerInfo{Server: srv, FullMethod: queryStatsMethod}
				return icpt(ctx, in, info, func(ctx context.Context, req interface{}) (interface{}, error) {
					return resp, nil
				})
			},
		}},
	}
	srv.RegisterService(&desc, nil) // 手写 desc 无 HandlerType，实现传 nil 免反射校验
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln, seen
}

func dialBufconn(ln *bufconn.Listener) grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return ln.DialContext(ctx)
	})
}

func TestQueryUserStats(t *testing.T) {
	resp := &QueryStatsResponse{Stat: []*Stat{
		{Name: "user>>>alice>>>traffic>>>uplink", Value: 120},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 340},
		{Name: "user>>>bob>>>traffic>>>downlink", Value: 5},
	}}
	ln, seen := startFakeStats(t, resp)
	c := newWithDial("xray-api", log.New(io.Discard, "", 0), dialBufconn(ln))

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

func TestQueryUserStatsConnRefused(t *testing.T) {
	// 关闭端口：查询要报错而不是静默空表。
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

func TestParseUserStats(t *testing.T) {
	got := ParseUserStats([]*Stat{
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 1},
		{Name: "user>>>a@ferry>>>traffic>>>uplink", Value: 2}, // 同名条目累加
		{Name: "user>>>a@ferry>>>traffic>>>downlink", Value: 10},
		{Name: "inbound>>>in-1>>>traffic>>>uplink", Value: 99}, // 非 user 条目忽略
		{Name: "user>>>b@ferry>>>traffic>>>unknown", Value: 7}, // 未知方向忽略
		{Name: "user>>>b@ferry>>>something", Value: 8},         // 段数不符忽略
		nil,                                                    // 空条目忽略
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
