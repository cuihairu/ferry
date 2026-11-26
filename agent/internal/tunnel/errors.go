package tunnel

import (
	"errors"
	"fmt"
)

// ErrorClass buckets a tunnel failure so callers can decide what to do with it
// (F-0823): retry the dial, reload config, or report a hostile environment.
type ErrorClass string

const (
	// ClassConfig means the plugin options are wrong (missing fields, unreadable
	// key/CA file): retrying with the same options cannot succeed.
	ClassConfig ErrorClass = "config"
	// ClassAuth means the peer refused us (handshake rejected, host key
	// mismatch, sidecar ERR reply): an environment/credential problem.
	ClassAuth ErrorClass = "auth"
	// ClassNetwork means transport-level trouble (dial/IO failure): worth
	// retrying, possibly after backoff.
	ClassNetwork ErrorClass = "network"
	// ClassHeartbeat means an established connection went silent (the shared
	// ping/pong declared it dead): the caller should reconnect.
	ClassHeartbeat ErrorClass = "heartbeat"
)

// Error is a classified tunnel failure: plugins wrap their errors in it and
// callers read the class back with Classify.
type Error struct {
	Class ErrorClass
	// Op identifies the failing step as "plugin:op" (e.g. "ssh:dial"); empty
	// for frame-level errors that have no plugin context.
	Op  string
	Err error
}

func (e *Error) Error() string {
	if e.Op == "" {
		return fmt.Sprintf("tunnel %s: %v", e.Class, e.Err)
	}
	return fmt.Sprintf("tunnel %s %s: %v", e.Op, e.Class, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Classify walks an error chain for the first *Error and returns its class;
// the shared ErrHeartbeatLost sentinel maps to ClassHeartbeat directly.
// Anything unclassified counts as network trouble -- the safe default for
// transport work, and the honest class for third-party plugin errors.
func Classify(err error) ErrorClass {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrHeartbeatLost) {
		return ClassHeartbeat
	}
	var te *Error
	if errors.As(err, &te) {
		return te.Class
	}
	return ClassNetwork
}

// Wrap tags err with a class and the plugin op, returning nil for a nil err
// so call sites can `return Wrap(...)` directly.
func Wrap(class ErrorClass, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Class: class, Op: op, Err: err}
}

// Errorf builds a classified error with a formatted message (no wrapped
// cause -- use Wrap when there is an underlying error).
func Errorf(class ErrorClass, op, format string, args ...any) error {
	return &Error{Class: class, Op: op, Err: fmt.Errorf(format, args...)}
}
