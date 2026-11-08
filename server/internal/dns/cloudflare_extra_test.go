package dns

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// errFake 可编程 Cloudflare API 替身：override 命中即按其返回（状态码+体），
// 未命中走最小正确路由（zone 恒 zid1、记录恒一条）。
func errFake(t *testing.T, override func(r *http.Request) (int, string)) *Cloudflare {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil {
			if code, body := override(r); code != 0 {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(body))
				return
			}
		}
		switch {
		case r.URL.Path == "/zones":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zid1"}]}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/zid1/dns_records"):
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"rid1","proxied":true}]}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"done"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &Cloudflare{Token: "tok", Base: srv.URL, HTTP: srv.Client()}
}

// TestCloudflareDoMarshalError 请求体不可序列化：组包即报错，不触网。
func TestCloudflareDoMarshalError(t *testing.T) {
	c := &Cloudflare{Token: "tok", Base: "http://127.0.0.1:1", HTTP: &http.Client{}}
	if _, err := c.do(context.Background(), http.MethodPost, "/zones",
		map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("unmarshalable body must error")
	}
}

type deadBody struct{}

func (deadBody) Read([]byte) (int, error) { return 0, errors.New("body gone") }
func (deadBody) Close() error             { return nil }

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestCloudflareDoBodyReadError 响应体读一半断流：按读失败原样上抛。
func TestCloudflareDoBodyReadError(t *testing.T) {
	c := &Cloudflare{Token: "tok", Base: "http://127.0.0.1:1", HTTP: &http.Client{
		Transport: rtFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
				Body: io.NopCloser(deadBody{})}, nil
		}),
	}}
	_, err := c.do(context.Background(), http.MethodGet, "/zones", nil)
	if err == nil || !strings.Contains(err.Error(), "body gone") {
		t.Fatalf("err = %v; want body read error", err)
	}
}

// TestCloudflareEmptyRecordName 空记录名：定位 zone 前即拒绝。
func TestCloudflareEmptyRecordName(t *testing.T) {
	c, _ := newFake(t)
	ctx := context.Background()
	if err := c.Upsert(ctx, "", "A", "1.2.3.4"); err == nil || !strings.Contains(err.Error(), "empty record name") {
		t.Fatalf("upsert err = %v", err)
	}
	if err := c.Delete(ctx, "   ", "A"); err == nil || !strings.Contains(err.Error(), "empty record name") {
		t.Fatalf("delete err = %v", err)
	}
}

// TestCloudflareZoneQueryFailure zone 查询失败：逐级上抛，不冒称无 zone。
func TestCloudflareZoneQueryFailure(t *testing.T) {
	c := errFake(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/zones" {
			return http.StatusInternalServerError, `{"success":false,"errors":[{"message":"kaput"}]}`
		}
		return 0, ""
	})
	err := c.Upsert(context.Background(), "edge.example.com", "A", "1.2.3.4")
	if err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Fatalf("err = %v; want zone query failure", err)
	}
}

// TestCloudflareZonesBadJSON zone 响应体不是数组：解包报错。
func TestCloudflareZonesBadJSON(t *testing.T) {
	c := errFake(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/zones" {
			return http.StatusOK, `{"success":true,"result":42}`
		}
		return 0, ""
	})
	if err := c.Upsert(context.Background(), "edge.example.com", "A", "1.2.3.4"); err == nil {
		t.Fatal("scalar zones result must fail unmarshal")
	}
}

// TestCloudflareUpsertRecordsFailures upsert 的记录查询两档失败：HTTP 500
// 与 result 非数组都报错，不走到写入。
func TestCloudflareUpsertRecordsFailures(t *testing.T) {
	ctx := context.Background()

	c := errFake(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/zid1/dns_records") {
			return http.StatusInternalServerError, `{"success":false,"errors":[{"message":"kaput"}]}`
		}
		return 0, ""
	})
	err := c.Upsert(ctx, "edge.example.com", "A", "1.2.3.4")
	if err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Fatalf("records 500 err = %v", err)
	}

	c = errFake(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/zid1/dns_records") {
			return http.StatusOK, `{"success":true,"result":"oops"}`
		}
		return 0, ""
	})
	if err := c.Upsert(ctx, "edge.example.com", "A", "1.2.3.4"); err == nil {
		t.Fatal("scalar records result must fail unmarshal")
	}
}

// TestCloudflareDeleteRecordsFailures delete 的记录查询两档失败与单条删除
// 失败：都报错不冒称撤干净。
func TestCloudflareDeleteRecordsFailures(t *testing.T) {
	ctx := context.Background()

	c := errFake(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/zid1/dns_records") {
			return http.StatusInternalServerError, `{"success":false,"errors":[{"message":"kaput"}]}`
		}
		return 0, ""
	})
	err := c.Delete(ctx, "edge.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "kaput") {
		t.Fatalf("records 500 err = %v", err)
	}

	c = errFake(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones/zid1/dns_records") {
			return http.StatusOK, `{"success":true,"result":"oops"}`
		}
		return 0, ""
	})
	if err := c.Delete(ctx, "edge.example.com", "A"); err == nil {
		t.Fatal("scalar records result must fail unmarshal")
	}

	c = errFake(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodDelete {
			return http.StatusInternalServerError, `{"success":false,"errors":[{"message":"del kaput"}]}`
		}
		return 0, ""
	})
	err = c.Delete(ctx, "edge.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "del kaput") {
		t.Fatalf("record delete 500 err = %v", err)
	}
}
