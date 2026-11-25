package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeStateSeparation covers F-0206: disabled (Enabled=false), maintenance,
// offline (Status) and unhealthy (PoolState) are independent flags; toggling
// each one leaves the other three untouched.
func TestNodeStateSeparation(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "state-node", "address": "state.example.com", "port": 443,
		"protocol": "vless", "maintenance": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["id"].(float64))

	var n storage.Node
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	if !n.Enabled || !n.Maintenance || n.Status != "unknown" || n.PoolState != "active" {
		t.Fatalf("initial state = %+v", n)
	}

	// Offline is a runtime status, independent of the admin flags.
	if err := db.Model(&storage.Node{}).Where("id = ?", id).Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	// Unhealthy is a pool verdict, independent of runtime connectivity.
	if err := db.Model(&storage.Node{}).Where("id = ?", id).Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	if !n.Enabled || !n.Maintenance || n.Status != "offline" || n.PoolState != "suspended" {
		t.Fatalf("four-state mix = %+v", n)
	}

	// Admin clears maintenance and disables: status/pool state survive.
	rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(id), map[string]any{
		"maintenance": false, "enabled": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	if n.Enabled || n.Maintenance || n.Status != "offline" || n.PoolState != "suspended" {
		t.Fatalf("after disable = %+v", n)
	}

	// Absent state fields keep their values on update (merge semantics).
	rec = doJSON(t, r, "PUT", "/api/nodes/"+utoa(id), map[string]any{"name": "state-node-2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("name-only update: %d %s", rec.Code, rec.Body)
	}
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	if n.Enabled || n.Maintenance || n.Status != "offline" || n.PoolState != "suspended" {
		t.Fatalf("after merge = %+v", n)
	}
}
