package cloudprov

import "sync"

// Provider-API authentication failure detection (F-1828): vendors reject bad
// credentials as non-retryable ErrAuth; this file funnels that fact to one
// process-wide hook so the alert leg (Herald) stays out of this
// dependency-free package. The wiring layer registers the sink where the
// database lives (main), every call path reports through here.

// AuthFailure is the identity plus context payload handed to the hook:
// identity comes from the Account the caller is working with, the detail
// from the classified *Error.
type AuthFailure struct {
	ProviderID   uint
	ProviderName string
	ProviderType string
	Op           string
	ResourceID   string
	ProviderCode string
	Message      string
}

var (
	authHookMu sync.Mutex
	authHook   func(AuthFailure)
)

// SetAuthFailureHook registers the auth-failure sink (F-1828); nil clears it.
// Re-registration replaces the previous sink.
func SetAuthFailureHook(fn func(AuthFailure)) {
	authHookMu.Lock()
	defer authHookMu.Unlock()
	authHook = fn
}

// ReportAuthFailure classifies err and, when the vendor rejected the
// credentials (ErrAuth), hands the fact to the hook. Everything else --
// no hook, other error types, nil errors -- is a no-op, so call sites can
// report unconditionally on error.
func ReportAuthFailure(a Account, op, resourceID string, err error) {
	if err == nil {
		return
	}
	e := AsError(err)
	if e.Type != ErrAuth {
		return
	}
	authHookMu.Lock()
	fn := authHook
	authHookMu.Unlock()
	if fn == nil {
		return
	}
	fn(AuthFailure{
		ProviderID: a.ProviderID, ProviderName: a.Name, ProviderType: a.Type,
		Op: op, ResourceID: resourceID, ProviderCode: e.ProviderCode, Message: e.Message,
	})
}
