// Outbound pacing for server-initiated calls (F-0047): external API surfaces
// (webhooks, DNS provider, payment/service APIs, IdP) share one per-host
// sliding-window pacer, so a burst of events cannot hammer a third-party
// endpoint. Same zero-dependency idiom as the inbound Limiter; unlike it,
// Wait blocks for a slot instead of rejecting -- the callers are background
// workers, where pacing beats failing.
package ratelimit

import (
	"context"
	"net/url"
	"sync"
	"time"
)

// Outbound defaults: per remote host at most OutboundMaxPerHost calls per
// OutboundWindow (~1 rps sustained). Enough headroom for notification fan-out
// and DNS record ops while staying under typical third-party quotas (e.g.
// Cloudflare's 1200 requests / 5 min).
const (
	OutboundWindow     = time.Minute
	OutboundMaxPerHost = 60
)

// Outbound paces calls per remote host.
type Outbound struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	max    int
	hosts  map[string][]time.Time
}

// OutboundDefault is the shared pacer every outbound HTTP surface goes through.
var OutboundDefault = NewOutbound(OutboundWindow, OutboundMaxPerHost)

// NewOutbound creates a pacer allowing max calls per host per window.
func NewOutbound(window time.Duration, max int) *Outbound {
	return &Outbound{now: time.Now, window: window, max: max, hosts: map[string][]time.Time{}}
}

// Wait takes one quota slot for host, blocking until the window frees a slot
// or ctx is done; the wait is bounded by the window (the oldest attempt ages
// out), so a Background context can never hang forever.
func (o *Outbound) Wait(ctx context.Context, host string) error {
	for {
		o.mu.Lock()
		now := o.now()
		st := o.hosts[host]
		if st != nil {
			st = prune(st, now.Add(-o.window))
			if len(st) == 0 {
				delete(o.hosts, host)
			} else {
				o.hosts[host] = st
			}
		}
		if len(st) < o.max {
			o.hosts[host] = append(st, now)
			o.mu.Unlock()
			return nil
		}
		until := st[0].Add(o.window)
		o.mu.Unlock()

		t := time.NewTimer(until.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Wait takes one slot from OutboundDefault for host.
func Wait(ctx context.Context, host string) error {
	return OutboundDefault.Wait(ctx, host)
}

// WaitURL waits on the remote host parsed out of a raw URL (unparseable input
// falls back to the raw string as the bucket key, so a slot is still taken).
func WaitURL(ctx context.Context, raw string) error {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		raw = u.Host
	}
	return OutboundDefault.Wait(ctx, raw)
}
