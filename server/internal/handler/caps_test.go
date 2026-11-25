package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeCapabilitiesAPI covers the panel side of F-0222~F-0227 + F-0231:
// a validated static list is stored with a history row, changes append more
// rows, and unknown names are rejected.
func TestNodeCapabilitiesAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "caps-a", "address": "a.example.com", "port": 443, "protocol": "vless",
		"role": "landing",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}
	id := utoa(uint(mustJSON(t, rec)["id"].(float64)))

	// Set the static list on update; an unknown name must fail first.
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+id, map[string]any{
		"capabilities": []string{"made_up"},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown capability = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+id, map[string]any{
		"capabilities": []string{"relay_landing", "tcp_forward", "relay_landing"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("set caps: %d %s", rec.Code, rec.Body)
	}
	node := mustJSON(t, rec)
	if node["capabilities"] != `["relay_landing","tcp_forward"]` {
		t.Fatalf("stored caps = %v", node["capabilities"])
	}

	// No dynamic state yet: the history has exactly the panel edit.
	var changes []storage.CapabilityChange
	if err := db.Where("node_id = ?", node["id"]).Order("id").Find(&changes).Error; err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Kind != "static" ||
		changes[0].Before != "[]" || changes[0].After != `["relay_landing","tcp_forward"]` ||
		changes[0].Reason != "panel update" {
		t.Fatalf("static history = %+v", changes)
	}

	// Replacing the list appends a second row (before carries the old list).
	if rec = doJSON(t, r, "PUT", "/api/nodes/"+id, map[string]any{
		"capabilities": []string{"relay_landing"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("replace caps: %d %s", rec.Code, rec.Body)
	}
	changes = nil
	if err := db.Where("node_id = ?", node["id"]).Order("id").Find(&changes).Error; err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[1].Before != `["relay_landing","tcp_forward"]` ||
		changes[1].After != `["relay_landing"]` {
		t.Fatalf("second history row = %+v", changes)
	}
}

// TestSyncCapsHelloAndHeartbeat drives the agent path directly (F-0228/F-0229/
// F-0231): the first hello report fills the empty list, a later one does not
// win against the panel, heartbeat states fold in with a history row per
// change, and garbage names are dropped.
func TestSyncCapsHelloAndHeartbeat(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if err := db.Create(&storage.Node{
		Name: "cap-agent", Token: "cap-agent", Role: "landing", Enabled: true, Status: "unknown",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var node storage.Node
	if err := db.Where("name = ?", "cap-agent").First(&node).Error; err != nil {
		t.Fatal(err)
	}

	// First hello: the agent's declared set is recorded with a history row.
	h.syncCaps(int64(node.ID), []string{"relay_landing", "bogus"})
	var got storage.Node
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Capabilities != `["relay_landing"]` {
		t.Fatalf("hello caps = %q", got.Capabilities)
	}
	var changes []storage.CapabilityChange
	db.Where("node_id = ?", node.ID).Order("id").Find(&changes)
	if len(changes) != 1 || changes[0].Kind != "static" || changes[0].Reason != "agent hello" {
		t.Fatalf("hello history = %+v", changes)
	}

	// Panel takes over with its own list; a diverging report must not win.
	if err := db.Model(&storage.Node{}).Where("id = ?", node.ID).
		Update("capabilities", `["relay_landing","probe"]`).Error; err != nil {
		t.Fatal(err)
	}
	h.syncCaps(int64(node.ID), []string{"relay_landing"})
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Capabilities != `["relay_landing","probe"]` {
		t.Fatalf("panel list overwritten: %q", got.Capabilities)
	}

	// Heartbeat: ok -> off folds in and appends a dynamic history row.
	h.mergeCaps(int64(node.ID), map[string]string{"relay_landing": "off", "nope": "ok"})
	if err := db.First(&got, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.CapabilityState != `{"relay_landing":"off"}` {
		t.Fatalf("merged state = %q", got.CapabilityState)
	}
	changes = nil
	db.Where("node_id = ? AND kind = ?", node.ID, "dynamic").Order("id").Find(&changes)
	if len(changes) != 1 || changes[0].Before != "" || changes[0].After != "off" ||
		changes[0].Reason != "heartbeat" {
		t.Fatalf("dynamic history = %+v", changes)
	}

	// Repeating the same state is a no-op (no extra rows).
	h.mergeCaps(int64(node.ID), map[string]string{"relay_landing": "off"})
	changes = nil
	db.Where("node_id = ? AND kind = ?", node.ID, "dynamic").Find(&changes)
	if len(changes) != 1 {
		t.Fatalf("unchanged heartbeat wrote %d rows", len(changes))
	}

	// Recovery: off -> ok appends the flip.
	h.mergeCaps(int64(node.ID), map[string]string{"relay_landing": "ok"})
	changes = nil
	db.Where("node_id = ? AND kind = ?", node.ID, "dynamic").Order("id").Find(&changes)
	if len(changes) != 2 || changes[1].Before != "off" || changes[1].After != "ok" {
		t.Fatalf("recovery history = %+v", changes)
	}
}
