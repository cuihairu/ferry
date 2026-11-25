package handler

import (
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathHealthEndpoints covers F-0251/F-0268/F-0269 over the API: the path
// health endpoint reflects a fresh probe verdict, and the group endpoint
// aggregates members with availability.
func TestPathHealthEndpoints(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
		// The created row starts offline-unset; force it online for the probes.
		id := uint(mustJSON(t, rec)["id"].(float64))
		if err := db.Model(&storage.Node{}).Where("id = ?", id).
			Updates(map[string]any{"status": "online", "pool_state": "active"}).Error; err != nil {
			t.Fatal(err)
		}
		return id
	}
	entry, landing := mkNode("hp-entry"), mkNode("hp-land")

	rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "hp-path", "entry_node_id": entry, "landing_node_id": landing,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path: %d %s", rec.Code, rec.Body)
	}
	pathID := uint(mustJSON(t, rec)["id"].(float64))

	// Unknown before any probe data.
	rec = doJSON(t, r, "GET", "/api/paths/"+utoa(pathID)+"/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("path health: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["health"]; got != "unknown" {
		t.Fatalf("no-data path health = %v", got)
	}

	// A fresh healthy probe flips it to healthy.
	target := landing
	if err := db.Create(&storage.ProbeReport{
		NodeID: entry, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/paths/"+utoa(pathID)+"/health", nil)
	if got := mustJSON(t, rec)["health"]; got != "healthy" {
		t.Fatalf("probed path health = %v", got)
	}

	// Group aggregation: one active member (healthy) and one excluded one.
	landB := mkNode("hp-land-b")
	rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "hp-path-b", "entry_node_id": entry, "landing_node_id": landB,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path b: %d %s", rec.Code, rec.Body)
	}
	pathB := uint(mustJSON(t, rec)["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/path-groups", map[string]any{
		"name": "hp-group", "direction": "out",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	groupID := uint(mustJSON(t, rec)["id"].(float64))
	for _, pid := range []uint{pathID, pathB} {
		rec = doJSON(t, r, "POST", "/api/path-groups/"+utoa(groupID)+"/members", map[string]any{
			"path_id": pid, "excluded": pid == pathB,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("add member %d: %d %s", pid, rec.Code, rec.Body)
		}
	}

	rec = doJSON(t, r, "GET", "/api/path-groups/"+utoa(groupID)+"/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("group health: %d %s", rec.Code, rec.Body)
	}
	body := mustJSON(t, rec)
	if body["health"] != "healthy" {
		t.Fatalf("group health = %v", body["health"])
	}
	avail, ok := body["availability"].(float64)
	if !ok || avail != 100 {
		t.Fatalf("availability = %v", body["availability"])
	}
	members, _ := body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("members = %v", body["members"])
	}

	// Missing rows answer 404.
	if rec = doJSON(t, r, "GET", "/api/paths/99999/health", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing path health = %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/path-groups/99999/health", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing group health = %d", rec.Code)
	}
}
