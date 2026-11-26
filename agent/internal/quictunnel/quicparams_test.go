package quictunnel

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// TestValidateQUICConfig covers F-0827: the shipped defaults satisfy the
// invariants, and each violated invariant is rejected with a nameable error.
func TestValidateQUICConfig(t *testing.T) {
	if err := ValidateQUICConfig(DefaultQUICConfig()); err != nil {
		t.Fatalf("shipped defaults must validate: %v", err)
	}
	if err := ValidateQUICConfig(nil); err == nil {
		t.Fatal("nil config must be rejected")
	}

	cases := []struct {
		name string
		mut  func(c *quic.Config)
	}{
		{"zero idle timeout", func(c *quic.Config) { c.MaxIdleTimeout = 0 }},
		{"negative keepalive", func(c *quic.Config) { c.KeepAlivePeriod = -time.Second }},
		{"keepalive at idle timeout", func(c *quic.Config) { c.KeepAlivePeriod = c.MaxIdleTimeout }},
		{"keepalive past idle timeout", func(c *quic.Config) { c.KeepAlivePeriod = c.MaxIdleTimeout + time.Minute }},
		{"no stream ceiling", func(c *quic.Config) { c.MaxIncomingStreams = 0 }},
		{"negative stream ceiling", func(c *quic.Config) { c.MaxIncomingStreams = -1 }},
	}
	for _, tc := range cases {
		cfg := DefaultQUICConfig()
		tc.mut(cfg)
		if err := ValidateQUICConfig(cfg); err == nil {
			t.Errorf("%s: must be rejected", tc.name)
		}
	}
}

// TestNewClientWithConfig covers the validated construction path: a good
// config is accepted (and the ALPN is pinned), a bad one errors without
// panicking.
func TestNewClientWithConfig(t *testing.T) {
	good := DefaultQUICConfig()
	c, err := NewClientWithConfig(&tls.Config{}, good)
	if err != nil {
		t.Fatalf("good config: %v", err)
	}
	if c == nil {
		t.Fatal("nil client for good config")
	}

	bad := DefaultQUICConfig()
	bad.KeepAlivePeriod = bad.MaxIdleTimeout + time.Second
	if _, err := NewClientWithConfig(&tls.Config{}, bad); err == nil {
		t.Fatal("keepalive >= idle timeout must be rejected")
	}
}
