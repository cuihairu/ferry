package pathhealth

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestRelayLossDegradesPath covers F-0834: a relay hop that lost contact
// (heartbeat timeout marks the node offline; pool suspension is the other
// loss signal) drags every path it carries into degraded.
func TestRelayLossDegradesPath(t *testing.T) {
	db := newTestDB(t)
	entry := mkNode(t, db, "rf-entry", "online", "active")
	landing := mkNode(t, db, "rf-landing", "online", "active")
	relay := mkNode(t, db, "rf-relay", "online", "active")
	rid := relay.ID
	path := storage.Path{Name: "rf-relayed", EntryNodeID: entry.ID, LandingNodeID: landing.ID, RelayNodeID: &rid}
	if err := db.Create(&path).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	seedVerdict(t, db, entry.ID, landing.ID, agentproto.ProbeVerdictHealthy, time.Minute)

	// All endpoints up and a fresh healthy verdict: healthy.
	if got := Evaluate(db, []storage.Path{path}, now)[path.ID]; got != Healthy {
		t.Fatalf("healthy baseline = %q", got)
	}

	// Relay hop loses heartbeat (node flips offline): degraded.
	if err := db.Model(&storage.Node{}).Where("id = ?", relay.ID).
		Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(db, []storage.Path{path}, now)[path.ID]; got != Degraded {
		t.Fatalf("relay offline = %q", got)
	}

	// Relay back online but pool-suspended: still degraded.
	if err := db.Model(&storage.Node{}).Where("id = ?", relay.ID).
		Updates(map[string]any{"status": "online", "pool_state": "suspended"}).Error; err != nil {
		t.Fatal(err)
	}
	if got := Evaluate(db, []storage.Path{path}, now)[path.ID]; got != Degraded {
		t.Fatalf("relay suspended = %q", got)
	}
}

// TestProcLivenessNeverMakesPathHealthy covers F-0836: a running relay
// process row is NOT availability evidence. Evaluate reads node liveness,
// pool state and probe verdicts only -- with everything "alive" but no fresh
// probe verdict, the path stays unknown instead of being called healthy.
func TestProcLivenessNeverMakesPathHealthy(t *testing.T) {
	db := newTestDB(t)
	entry := mkNode(t, db, "pl-entry", "online", "active")
	landing := mkNode(t, db, "pl-landing", "online", "active")
	relay := mkNode(t, db, "pl-relay", "online", "active")
	rid := relay.ID
	path := storage.Path{Name: "pl-relayed", EntryNodeID: entry.ID, LandingNodeID: landing.ID, RelayNodeID: &rid}
	if err := db.Create(&path).Error; err != nil {
		t.Fatal(err)
	}

	// Every relay process on every hop reports running.
	for _, nid := range []uint{entry.ID, landing.ID, relay.ID} {
		if err := db.Create(&storage.NodeProcStatus{
			NodeID: nid, Proc: "relay", State: "running", PID: 100, UpdatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	// No probe verdict at all: unknown -- proc liveness alone proves nothing.
	if got := Evaluate(db, []storage.Path{path}, now)[path.ID]; got != Unknown {
		t.Fatalf("running procs, no verdict = %q (must not be healthy)", got)
	}

	// Even a sick verdict outranks the running procs.
	seedVerdict(t, db, entry.ID, landing.ID, agentproto.ProbeVerdictSick, time.Minute)
	if got := Evaluate(db, []storage.Path{path}, now)[path.ID]; got != Degraded {
		t.Fatalf("running procs, sick verdict = %q", got)
	}
}
