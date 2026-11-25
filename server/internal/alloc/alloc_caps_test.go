package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSweepCapabilityGate covers F-0230: allocation only lands on nodes that
// declare the needed capability and have not reported it off.
func TestSweepCapabilityGate(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "cap-a", "entry", "out", nil)

	// This landing's panel list omits relay_landing: it must never be picked.
	seedNode(t, db, "cap-land-plain", "landing", "out", func(n *storage.Node) {
		n.Capabilities = `["proxy_ingress","tcp_forward"]`
	})
	seedNode(t, db, "cap-land-ok", "landing", "out", nil)

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "cap-land-ok" {
		t.Fatalf("sweep without relay_landing events = %+v", events)
	}

	// Release and re-run with the only capable landing reporting it off:
	// nothing is assignable this round.
	var okLand storage.Node
	if err := db.Where("name = ?", "cap-land-ok").First(&okLand).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", okLand.ID).
		Update("capability_state", `{"relay_landing":"off"}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("1 = 1").Delete(&storage.LandingAssignment{}).Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("off-state sweep events = %+v", events)
	}
}

// TestSweepEntryCapabilityGate covers the entry side of F-0230: an entry that
// does not declare relay_entry allocates nothing.
func TestSweepEntryCapabilityGate(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "cap-ent-plain", "entry", "out", func(n *storage.Node) {
		n.Capabilities = `["probe"]`
	})
	seedNode(t, db, "cap-land-b", "landing", "out", nil)

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("entry without relay_entry allocated: %+v", events)
	}
}
