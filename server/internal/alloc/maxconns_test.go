package alloc

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSweepMaxConnsCap covers the connection cap (F-1065): a candidate at or
// above its own cap is ruled out of the round (the cheap one would otherwise
// win under cost_first) with the numbers in the exclusion trail, while a
// landing under its cap stays eligible.
func TestSweepMaxConnsCap(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "mc-entry", "entry", "out", nil)
	capped := seedNode(t, db, "mc-cheap", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 5
		n.MaxConns = 100
	})
	open := seedNode(t, db, "mc-open", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 50
	})

	// at the cap: the cheap landing is full, the sweep takes the open one
	seedConns(t, db, capped.ID, "xray", 100)
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != open.Name {
		t.Fatalf("capped landing must be skipped: %+v", events)
	}
	var ex storage.AllocExclusion
	if err := db.Where("entry_node_id = ? AND landing_node_id = ?", entry.ID, capped.ID).
		Order("id DESC").First(&ex).Error; err != nil {
		t.Fatalf("cap exclusion missing: %v", err)
	}
	if !strings.Contains(ex.Reason, "达上限 100") {
		t.Fatalf("exclusion reason = %s", ex.Reason)
	}

	// under the cap: the cheap landing wins again
	if err := db.Where("1 = 1").Delete(&storage.LandingAssignment{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", capped.ID).Update("max_conns", 200).Error; err != nil {
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
