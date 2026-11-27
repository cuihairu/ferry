package cloudprov

import (
	"context"
	"testing"
)

// TestAuthFailureHook covers F-1828: a client call the vendor rejects with
// ErrAuth reaches the hook with the caller's account identity and the error
// detail; other error types stay silent; a nil hook is a safe no-op; clearing
// the hook stops the reports. The hook is process-wide, so it is cleared via
// defer before the test returns.
func TestAuthFailureHook(t *testing.T) {
	var got []AuthFailure
	SetAuthFailureHook(func(f AuthFailure) { got = append(got, f) })
	defer SetAuthFailureHook(nil)

	f := NewFake()
	authErr := func() *Error { return NewError(ErrAuth, OpRefreshStatus, "", "401", "invalid api key") }
	f.SetFailHook(OpRefreshStatus, authErr)
	c := NewClient(f, ClientOptions{
		Account:       Account{ProviderID: 7, Name: "prov7", Type: "fake"},
		MaxRetries:    0,
		MinAPIVersion: "1.0",
	})
	if _, err := c.RefreshStatus(context.Background(), "r1"); err == nil {
		t.Fatal("auth failure must surface")
	}
	if len(got) != 1 {
		t.Fatalf("hook calls = %d, want 1", len(got))
	}
	if got[0].ProviderID != 7 || got[0].ProviderName != "prov7" ||
		got[0].Op != OpRefreshStatus || got[0].ProviderCode != "401" {
		t.Fatalf("failure payload = %+v", got[0])
	}

	// A non-auth failure (vendor unavailable) must not report.
	f.SetFailHook(OpRefreshStatus, func() *Error {
		return NewError(ErrUnavailable, OpRefreshStatus, "", "503", "vendor down")
	})
	if _, err := c.RefreshStatus(context.Background(), "r1"); err == nil {
		t.Fatal("unavailable must surface")
	}
	if len(got) != 1 {
		t.Fatalf("non-auth failure reported: %+v", got)
	}

	// Without the hook, reporting is a no-op (no panic, error still returned).
	SetAuthFailureHook(nil)
	f.SetFailHook(OpRefreshStatus, authErr)
	if _, err := c.RefreshStatus(context.Background(), "r1"); err == nil {
		t.Fatal("auth failure must surface without a hook")
	}
	if len(got) != 1 {
		t.Fatalf("cleared hook still reported: %+v", got)
	}
}
