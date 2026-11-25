package handler

import (
	"sync"
	"time"
)

// Idempotency for report messages (FERRY-004): agent->panel report-style messages
// guarantee at-most-once side effects keyed by (node, type, envelope ID) -- a duplicate
// frame skips persistence/accounting but still gets its ack. Definitions and the
// executable criteria are in `docs/design/agent架构设计.md` §8. Agents currently discard on send
// failure (no retransmission), so this layer is contract surface + defense; an empty ID is
// never deduplicated (forward compatibility with old agents' proc_report).
const (
	dedupeCap = 512              // window capacity per node (FIFO, evicted entries become invalid)
	dedupeTTL = 10 * time.Minute // how long a window entry lives, on the same scale as the node token rotation grace period
)

// reportDeduper is one node's idempotency window: a bounded FIFO + TTL, and whichever bound
// hits first invalidates the entry.
// Its lifetime lives with the Handler (the panel process), so it survives agent reconnects and
// is cleared on a panel restart.
type reportDeduper struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	seen  map[string]time.Time
	order []string // FIFO eviction order; an entry may already have been deleted by the TTL path, so popping checks seen
}

func newReportDeduper() *reportDeduper {
	return newReportDeduperCap(dedupeCap, dedupeTTL)
}

func newReportDeduperCap(capacity int, ttl time.Duration) *reportDeduper {
	return &reportDeduper{cap: capacity, ttl: ttl, seen: make(map[string]time.Time)}
}

// repeat records a frame and reports whether it is a duplicate inside the window; an empty ID
// always returns false and is not recorded.
func (d *reportDeduper) repeat(msgType, id string) bool {
	if id == "" {
		return false
	}
	key := msgType + "\x00" + id
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if at, ok := d.seen[key]; ok {
		if now.Sub(at) < d.ttl {
			return true
		}
		delete(d.seen, key) // TTL expired: re-record it as a fresh frame
	}
	for len(d.seen) >= d.cap {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	d.seen[key] = now
	d.order = append(d.order, key)
	return false
}

// repeatReport records an agent report frame and reports whether it is a duplicate inside the
// idempotency window (bucketed per node).
func (h *Handler) repeatReport(nodeID int64, msgType, id string) bool {
	if id == "" {
		return false
	}
	v, _ := h.dedupe.LoadOrStore(nodeID, newReportDeduper())
	return v.(*reportDeduper).repeat(msgType, id)
}
