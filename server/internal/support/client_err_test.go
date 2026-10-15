package support

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 二十三批补口：servify 客户端错误分支——缺省 HTTP 客户端、建档非冲突错误、
// 409 回落失败两种形态、HTTPError 文案、URL 解析/拨号/读体失败与载荷不可编码。

// errReader 读即报错（注错响应体读取失败）。
type errReader struct{ err error }

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read fail") }
func (errReader) Close() error             { return nil }

// errTransport 返回一个 200 响应但响应体不可读。
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: errReader{}}, nil
}

// deadAddr 返回一个必被拒的本地地址（已关闭的监听端口）。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listen: %v", err)
	}
	return addr
}

func errSnap() CustomerSnapshot {
	return CustomerSnapshot{Email: "x@users.ferry.local", Name: "carol", Notes: "状态：启用"}
}

// TestHTTPDefaultClient HTTP 字段留空走缺省客户端（5s 超时），调用照常成功。
func TestHTTPDefaultClient(t *testing.T) {
	s := newStubServify(t)
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"} // HTTP 留空
	tok, exp, err := cl.GuestToken(context.Background(), "sess-1")
	if err != nil || tok == "" || exp <= 0 {
		t.Fatalf("GuestToken = %q %d err=%v", tok, exp, err)
	}
}

// TestSyncCustomerCreateErrors 建档三种失败：非 409 原样冒错、409 后回落
// 再查失败、409 后回落查无档案。
func TestSyncCustomerCreateErrors(t *testing.T) {
	// 建档非 409：错误原样冒给调用方。
	{
		s := newStubServify(t)
		s.onCreate = func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(http.StatusBadGateway)
		}
		cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
		err := cl.SyncCustomer(context.Background(), errSnap())
		if err == nil || !strings.Contains(err.Error(), "servify 502") {
			t.Fatalf("err = %v, want 502 HTTPError", err)
		}
	}

	// 409 后回落再查失败：按查询错误冒错。
	{
		s := newStubServify(t)
		s.onCreate = func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(http.StatusConflict)
		}
		cl := &Client{BaseURL: "http://" + deadAddr(t), ServiceKey: "sk-test"}
		if err := cl.SyncCustomer(context.Background(), errSnap()); err == nil {
			t.Fatal("want re-search error")
		}
	}

	// 409 后回落查无档案：冲突但查不到，冒包装错误。
	{
		s := newStubServify(t)
		s.onCreate = func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(http.StatusConflict) // 不建档案
		}
		cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
		err := cl.SyncCustomer(context.Background(), errSnap())
		if err == nil || !strings.Contains(err.Error(), "not found on re-search") {
			t.Fatalf("err = %v, want not found on re-search", err)
		}
	}
}

// TestHTTPErrorText 调用错误文案带 servify 状态码与响应体。
func TestHTTPErrorText(t *testing.T) {
	got := (&HTTPError{Status: http.StatusConflict, Body: "email already used"}).Error()
	want := "servify 409: email already used"
	if got != want {
		t.Fatalf("Error = %q, want %q", got, want)
	}
}

// TestCallBadRequests call 的四类请求侧失败：URL 解析、拨号、读响应体、
// 载荷不可编码。
func TestCallBadRequests(t *testing.T) {
	ctx := context.Background()

	// URL 解析失败（BaseURL 非法）。
	cl := &Client{BaseURL: "http://bad url"}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want URL parse error")
	}

	// 拨号失败（目标无监听）。
	cl = &Client{BaseURL: "http://" + deadAddr(t)}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want dial error")
	}

	// 响应体读取失败（注错 Reader）。
	cl = &Client{BaseURL: "http://x", HTTP: &http.Client{Transport: errTransport{}}}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want body read error")
	}

	// 载荷不可编码（json.Marshal 失败）。
	if err := cl.call(ctx, http.MethodPost, "/x", map[string]any{"fn": func() {}}, nil); err == nil {
		t.Fatal("want marshal error")
	}
}

// TestServerSideReadFailure 服务端回非 2xx：错误带状态码（调用面契约）。
func TestServerSideReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	cl := &Client{BaseURL: srv.URL, ServiceKey: "sk-test"}
	_, _, err := cl.GuestToken(context.Background(), "sess-1")
	if err == nil || !strings.Contains(err.Error(), "servify 503") {
		t.Fatalf("err = %v, want servify 503", err)
	}
}
