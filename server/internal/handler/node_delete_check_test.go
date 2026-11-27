package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeDeleteDependencyChecks covers F-0214: a node referenced by paths or
// active landing assignments, or with a running recovery, is refused; history
// rows (traffic/probes/alerts) are kept after deletion.
func TestNodeDeleteDependencyChecks(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
		return uint(mustJSON(t, rec)["id"].(float64))
	}
	entry, landing, other := mkNode("del-entry"), mkNode("del-landing"), mkNode("del-other")

	// Path reference blocks deletion (any of entry/landing/relay).
	rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "del-path", "entry_node_id": entry, "landing_node_id": other,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(entry), map[string]any{"confirm": "del-entry"}); rec.Code != http.StatusConflict {
		t.Fatalf("path-referenced delete = %d %s", rec.Code, rec.Body)
	}

	// Active landing assignment blocks deletion (either side).
	now := time.Now()
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry, LandingNodeID: landing, Direction: "out",
		Strategy: "manual", AssignedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(entry), map[string]any{"confirm": "del-entry"}); rec.Code != http.StatusConflict {
		t.Fatalf("assigned delete = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(landing), map[string]any{"confirm": "del-landing"}); rec.Code != http.StatusConflict {
		t.Fatalf("landing delete = %d %s", rec.Code, rec.Body)
	}

	// A released assignment does not block: a fresh node with a released
	// assignment deletes fine.
	released := now.Add(time.Minute)
	free := mkNode("del-free")
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry, LandingNodeID: free, Direction: "out",
		Strategy: "manual", AssignedAt: now,
		ReleasedAt: &released, ReleaseReason: "test",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(free), map[string]any{"confirm": "del-free"}); rec.Code != http.StatusNoContent {
		t.Fatalf("released assignment blocks delete = %d %s", rec.Code, rec.Body)
	}

	// Release the original entry→landing assignment too.
	if err := db.Model(&storage.LandingAssignment{}).Where("landing_node_id = ?", landing).
		Updates(map[string]any{"released_at": released, "release_reason": "test"}).Error; err != nil {
		t.Fatal(err)
	}

	// Running recovery blocks deletion.
	if err := db.Create(&storage.Recovery{
		NodeID: uint(landing), NodeName: "del-landing", State: recovery.StateRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(landing), map[string]any{"confirm": "del-landing"}); rec.Code != http.StatusConflict {
		t.Fatalf("recovery-running delete = %d %s", rec.Code, rec.Body)
	}
	if err := db.Model(&storage.Recovery{}).Where("node_id = ?", landing).
		Update("state", recovery.StateDone).Error; err != nil {
		t.Fatal(err)
	}

	// With every blocker cleared, deletion succeeds and history stays.
	nodeID := landing
	if err := db.Create(&storage.TrafficLog{
		NodeID: &nodeID, RxBytes: 100, TxBytes: 50, RecordedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.ProbeReport{
		NodeID: entry, TargetKind: "peer", TargetNodeID: &nodeID, Direction: "out",
		Verdict: "healthy", Region: "未知", ISP: "未知",
		ProbedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "DELETE", "/api/nodes/"+utoa(landing), map[string]any{"confirm": "del-landing"}); rec.Code != http.StatusNoContent {
		t.Fatalf("unblocked delete = %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.Node{}).Where("id = ?", landing).Count(&n)
	if n != 0 {
		t.Fatalf("node row survived delete")
	}
	// TrafficLog carries OnDelete:SET NULL: the row is retained, only its node
	// attribution is cleared.
	db.Model(&storage.TrafficLog{}).Count(&n)
	if n != 1 {
		t.Fatalf("traffic history dropped: %d", n)
	}
	db.Model(&storage.ProbeReport{}).Where("target_node_id = ?", landing).Count(&n)
	if n != 1 {
		t.Fatalf("probe history dropped: %d", n)
	}
}
