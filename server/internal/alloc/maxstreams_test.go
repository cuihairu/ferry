package alloc

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSweepMaxStreamsCap covers the concurrent stream cap (F-1066): in the P0
// single-stream data plane the live connection count is the stream load, so a
// candidate whose load has reached its declared ceiling is ruled out with the
// numbers in the trail, and drops the cap when the load falls back under it.
func TestSweepMaxStreamsCap(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "ms-entry", "entry", "out", nil)
	capped := seedNode(t, db, "ms-cheap", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 5
		n.MaxStreams = 50
	})
	open := seedNode(t, db, "ms-open", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 50
	})

	seedConns(t, db, capped.ID, "ferry", 50)
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != open.Name {
		t.Fatalf("stream-capped landing must be skipped: %+v", events)
	}
	var ex storage.AllocExclusion
	if err := db.Where("entry_node_id = ? AND landing_node_id = ?", entry.ID, capped.ID).
		Order("id DESC").First(&ex).Error; err != nil {
		t.Fatalf("stream cap exclusion missing: %v", err)
	}
	if !strings.Contains(ex.Reason, "并发流") || !strings.Contains(ex.Reason, "达上限 50") {
		t.Fatalf("exclusion reason = %s", ex.Reason)
	}

	// load back under the ceiling: the cheap landing wins again
	if err := db.Where("1 = 1").Delete(&storage.LandingAssignment{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", capped.ID).Update("max_streams", 100).Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != capped.Name {
		t.Fatalf("under-cap landing must win cost_first: %+v", events)
	}
}
