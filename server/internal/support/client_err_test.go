package support

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Batch 23 error paths: servify client error branches——default HTTP client, non-conflict create errors,
// the two shapes of the 409 fallback failure, HTTPError text, URL parse/dial/body-read failures, and an unencodable payload.

// errReader fails as soon as it is read (injecting a response-body read failure).
type errReader struct{ err error }

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read fail") }
func (errReader) Close() error             { return nil }

// errTransport returns a 200 response whose body cannot be read.
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: errReader{}}, nil
}

// deadAddr returns a local address that is guaranteed to be refused (a closed listener port).
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

// TestHTTPDefaultClient leaves the HTTP field empty to use the default client (5s timeout); the call still succeeds.
func TestHTTPDefaultClient(t *testing.T) {
	s := newStubServify(t)
	cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"} // HTTP left empty
	tok, exp, err := cl.GuestToken(context.Background(), "sess-1")
	if err != nil || tok == "" || exp <= 0 {
		t.Fatalf("GuestToken = %q %d err=%v", tok, exp, err)
	}
}

// TestSyncCustomerCreateErrors covers the three create failures: non-409 error surfaced as-is, the
// fallback re-query failing after a 409, and the fallback finding no profile after a 409.
func TestSyncCustomerCreateErrors(t *testing.T) {
	// Create error that is not 409: the error is surfaced to the caller as-is.
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

	// The first query fails: no create happens, and the query error is surfaced to the caller as-is.
	{
		cl := &Client{BaseURL: "http://" + deadAddr(t), ServiceKey: "sk-test"}
		if err := cl.SyncCustomer(context.Background(), errSnap()); err == nil {
			t.Fatal("want first-search error")
		}
	}

	// Fallback re-query fails after a 409: the first query hits an empty table, the create returns 409, and the fallback re-query gives 500——surfaced as the query error.
	{
		var searches int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if atomic.AddInt32(&searches, 1) > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
		}))
		t.Cleanup(srv.Close)
		cl := &Client{BaseURL: srv.URL, ServiceKey: "sk-test"}
		err := cl.SyncCustomer(context.Background(), errSnap())
		if err == nil || !strings.Contains(err.Error(), "500") {
			t.Fatalf("err = %v, want re-search 500", err)
		}
	}

	// Fallback finds no profile after a 409: a conflict with nothing to find, surfacing a wrapped error.
	{
		s := newStubServify(t)
		s.onCreate = func(w http.ResponseWriter, _ map[string]any) {
			w.WriteHeader(http.StatusConflict) // do not create a profile
		}
		cl := &Client{BaseURL: s.srv.URL, ServiceKey: "sk-test"}
		err := cl.SyncCustomer(context.Background(), errSnap())
		if err == nil || !strings.Contains(err.Error(), "not found on re-search") {
			t.Fatalf("err = %v, want not found on re-search", err)
		}
	}
}

// TestHTTPErrorText: the call error text carries the servify status code and response body.
func TestHTTPErrorText(t *testing.T) {
	got := (&HTTPError{Status: http.StatusConflict, Body: "email already used"}).Error()
	want := "servify 409: email already used"
	if got != want {
		t.Fatalf("Error = %q, want %q", got, want)
	}
}

// TestCallBadRequests covers the four client-side failures of call: URL parsing, dialing, reading the response body,
// and an unencodable payload.
func TestCallBadRequests(t *testing.T) {
	ctx := context.Background()

	// URL parsing fails (invalid BaseURL).
	cl := &Client{BaseURL: "http://bad url"}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want URL parse error")
	}

	// Dialing fails (nothing listening on the target).
	cl = &Client{BaseURL: "http://" + deadAddr(t)}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want dial error")
	}

	// Reading the response body fails (injected Reader).
	cl = &Client{BaseURL: "http://x", HTTP: &http.Client{Transport: errTransport{}}}
	if err := cl.call(ctx, http.MethodGet, "/x", nil, nil); err == nil {
		t.Fatal("want body read error")
	}

	// The payload cannot be encoded (json.Marshal fails).
	if err := cl.call(ctx, http.MethodPost, "/x", map[string]any{"fn": func() {}}, nil); err == nil {
		t.Fatal("want marshal error")
	}
}

// TestServerSideReadFailure: the server replies non-2xx; the error carries the status code (call-surface contract).
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
