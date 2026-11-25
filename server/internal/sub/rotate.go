// Subscription entry order (E-26): reorder by probe verdict——known latency ascending up front,
// entries without probe data last, and entries with the same verdict (same RTT tier) rotate.
// Clients often take the first subscription entry, so rotation spreads first-slot affinity
// across entries of the same tier; with no probe data keep the original order (no rotation, same as E-19).
// Scope: v2ray subscriptions only——clients consume the list order directly; the clash
// url-test group picks by its own latency self-test, so server-side ordering has no consumer and stays untouched.
package sub

import (
	"sort"
	"time"
)

// rotateWindowSec is the rotation period (seconds) for entries in the same RTT tier:
// order is fixed inside the window and shifts one step across windows; the slot derives statelessly from time and is not persisted on the subscription side.
const rotateWindowSec = 600

// orderByProbe returns a copy reordered by probe verdict: region grouping (stable, consistent with the
// v2ray grouping semantics), ascending known RTT within group, no data (0) last, rotation within
// the same RTT tier. now is injected for tests.
func orderByProbe(entries []Entry, now time.Time) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		return entryRegion(&out[i]) < entryRegion(&out[j])
	})
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && entryRegion(&out[end]) == entryRegion(&out[start]) {
			end++
		}
		orderSegment(out[start:end], now)
		start = end
	}
	return out
}

// orderSegment sorts one region segment by probe verdict and rotates the same tier (in place).
func orderSegment(seg []Entry, now time.Time) {
	// Known tier (rtt>0) first by ascending RTT, unknown tier (rtt==0) last keeping original order.
	sort.SliceStable(seg, func(i, j int) bool {
		return probeTier(seg[i]) < probeTier(seg[j]) ||
			(probeTier(seg[i]) == probeTier(seg[j]) && seg[i].RttMs < seg[j].RttMs)
	})
	for start := 0; start < len(seg); {
		end := start + 1
		for end < len(seg) && sameVerdict(seg[start], seg[end]) {
			end++
		}
		// Only the known tier rotates: the unknown tier has no comparable verdict, keep original order.
		if seg[start].RttMs > 0 {
			offset := int(now.Unix()/rotateWindowSec) % (end - start)
			rotateRun(seg, start, end, offset)
		}
		start = end
	}
}

// probeTier probe verdict tier: 0=verdict known (RTT known), 1=no verdict.
func probeTier(e Entry) int {
	if e.RttMs > 0 {
		return 0
	}
	return 1
}

// sameVerdict reports whether two entries share a verdict (same tier, same RTT).
func sameVerdict(a, b Entry) bool {
	return a.RttMs == b.RttMs
}

// rotateRun shifts the segment [start,end) by offset positions (in place).
func rotateRun(seg []Entry, start, end, offset int) {
	n := end - start
	if n <= 1 {
		return
	}
	offset %= n
	if offset < 0 {
		offset += n
	}
	if offset == 0 {
		return
	}
	tmp := make([]Entry, n)
	copy(tmp, seg[start:end])
	for i := 0; i < n; i++ {
		seg[start+i] = tmp[(i+offset)%n]
	}
}
