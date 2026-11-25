// Package xray reserves the interface for hooking up the Xray core.
// The current milestone only defines the boundary and does not implement any real integration; this interface can later
// be implemented on top of the gRPC API, or a no-op implementation can run the pure subscription-management flow.
package xray

import "context"

// Handler describes the minimal set of capabilities ferry needs from the Xray core.
// Traffic queries are used for billing, and inbound management is used to make node configuration take effect.
type Handler interface {
	// QueryUserTraffic returns the cumulative rx/tx bytes for the given user identifier since the last query.
	// email is the Xray inbound user email convention format, and the caller is responsible for assembling it.
	QueryUserTraffic(ctx context.Context, email string) (rx, tx uint64, err error)
	// AddInbound applies one inbound configuration to the core; cfg is an Xray inbound JSON fragment.
	AddInbound(ctx context.Context, cfg []byte) error
	// RemoveInbound removes an inbound by tag.
	RemoveInbound(ctx context.Context, tag string) error
}

// NoopHandler is a do-nothing empty implementation, for deployment shapes that have not hooked up a core.
type NoopHandler struct{}

func (NoopHandler) QueryUserTraffic(context.Context, string) (uint64, uint64, error) {
	return 0, 0, nil
}
func (NoopHandler) AddInbound(context.Context, []byte) error    { return nil }
func (NoopHandler) RemoveInbound(context.Context, string) error { return nil }
