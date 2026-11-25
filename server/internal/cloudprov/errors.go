package cloudprov

import (
	"fmt"
	"strings"
)

// ErrorType classifies provider failures into ferry-wide categories
// (F-0315): callers branch on Type, adapters own the vendor-code mapping.
type ErrorType string

const (
	// ErrAuth: credentials missing/invalid/expired. Not retryable.
	ErrAuth ErrorType = "auth"
	// ErrRateLimited: the vendor throttled us. Retryable with backoff.
	ErrRateLimited ErrorType = "rate_limited"
	// ErrNotFound: no such resource/account. Not retryable.
	ErrNotFound ErrorType = "not_found"
	// ErrUnsupported: the vendor or resource does not support the operation
	// (capability flag false, or the resource kind rejects it). Not retryable.
	ErrUnsupported ErrorType = "unsupported"
	// ErrUnavailable: the vendor API is down, timing out, or answering 5xx.
	// Retryable.
	ErrUnavailable ErrorType = "unavailable"
	// ErrBadRequest: the request is malformed or references an invalid
	// parameter (bad image name, bad window). Not retryable.
	ErrBadRequest ErrorType = "bad_request"
	// ErrCompat: the vendor API version is outside the adapter's supported
	// range (F-0317). Not retryable.
	ErrCompat ErrorType = "compat"
	// ErrInternal: anything unclassifiable. Not retryable by default.
	ErrInternal ErrorType = "internal"
)

// Retryable reports whether a Client retry may help this error type (F-0316).
func (t ErrorType) Retryable() bool {
	return t == ErrRateLimited || t == ErrUnavailable
}

// Error is the adapter-layer failure type (F-0315): every Adapter method
// fails with one of these, carrying the ferry type, the operation, the
// resource, the vendor's own code, and the human message.
type Error struct {
	Type         ErrorType `json:"type"`
	Op           string    `json:"op"` // e.g. "power_on", "list_resources"
	ResourceID   string    `json:"resource_id,omitempty"`
	ProviderCode string    `json:"provider_code,omitempty"` // vendor error code, verbatim
	Message      string    `json:"message,omitempty"`
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider %s: %s", e.Op, e.Type)
	if e.ProviderCode != "" {
		fmt.Fprintf(&b, " (%s)", e.ProviderCode)
	}
	if e.ResourceID != "" {
		fmt.Fprintf(&b, " resource=%s", e.ResourceID)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	return b.String()
}

// NewError builds an *Error; op uses the snake_case method names below.
func NewError(t ErrorType, op, resourceID, providerCode, message string) *Error {
	return &Error{Type: t, Op: op, ResourceID: resourceID, ProviderCode: providerCode, Message: message}
}

// Well-known operation names used in Error.Op (keep in sync with Adapter).
const (
	OpCapabilities  = "capabilities"
	OpListResources = "list_resources"
	OpDescribe      = "describe_resource"
	OpRefreshStatus = "refresh_status"
	OpPowerOn       = "power_on"
	OpPowerOff      = "power_off"
	OpReboot        = "reboot"
	OpForceStop     = "force_stop"
	OpRebuild       = "rebuild"
	OpImportBilling = "import_billing"
)

// ErrUnsupportedOp is the canonical failure for an operation the adapter does
// not declare in Capabilities (F-0303's fail-closed half).
func ErrUnsupportedOp(op, resourceID string) *Error {
	return &Error{Type: ErrUnsupported, Op: op, ResourceID: resourceID,
		Message: "operation not supported by this provider"}
}

// AsError extracts an *Error from err (nil when err is not one).
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Type: ErrInternal, Message: err.Error()}
}
