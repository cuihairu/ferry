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

// errFake is a programmable Cloudflare API stand-in: on an override hit it returns as configured
// (status code + body), otherwise it takes a minimal correct route (zone always zid1, records always one).
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

// TestCloudflareDoMarshalError: an unserializable request body errors at packing time, without any network call.
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

// TestCloudflareDoBodyReadError: the response body breaks mid-read; the read failure is surfaced as is.
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

// TestCloudflareEmptyRecordName: an empty record name is rejected before zone location.
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

// TestCloudflareZoneQueryFailure: a zone query failure is escalated level by level, never claiming no zone.
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

// TestCloudflareZonesBadJSON: a zone response body that is not an array errors at unpacking.
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

// TestCloudflareUpsertRecordsFailures: the two failure tiers of upsert's record query - an HTTP 500
// and a non-array result both error out, without ever reaching the write.
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

// TestCloudflareDeleteRecordsFailures: the two failure tiers of delete's record query and a single
// deletion failure - all error out rather than claiming a clean withdrawal.
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
