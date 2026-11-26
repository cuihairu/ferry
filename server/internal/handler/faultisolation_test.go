package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestRelayCrashDoesNotBlockNodeManagement covers F-0832/F-0836: a crashed
// relay process status never blocks the server's node management surface, and
// a live relay process alone is not enough to make the node's tri-state look
// healthy — the status endpoint keeps the crashed state visible.
func TestRelayCrashDoesNotBlockNodeManagement(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	node := storage.Node{Name: "fi-relay", Token: "tk-fi-relay", Role: "relay",
		Status: "online", Enabled: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := db.Create(&storage.NodeProcStatus{
		NodeID: node.ID, Proc: "relay", State: "crashed", Restarts: 3,
		UpdatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("proc status: %v", err)
	}

	// CRUD still works with a crashed relay row in the database.
	rec := doJSON(t, r, http.MethodGet, "/api/nodes", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fi-relay") {
		t.Fatalf("node list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+itoa(node.ID), map[string]any{
		"name": "fi-relay", "address": "relay.example.com", "port": 443,
		"protocol": "vless", "config": `{}`, "note": "maintenance while relay crashed",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("node update: code=%d body=%s", rec.Code, rec.Body.String())
	}
	// The proc command endpoint is still reachable: with no live agent link it
	// returns the explicit offline-agent 502 rather than failing because of the
	// crashed relay row in the DB (the management plane stays reachable).
	rec = doJSON(t, r, http.MethodPost, "/api/nodes/"+itoa(node.ID)+"/proc", map[string]any{
		"proc": "relay", "action": "start",
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("proc command must report offline agent: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Tri-state still shows the crashed process (F-0836): nothing infers
	// health from mere process liveness or from the management commands above.
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(node.ID)+"/status", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"crashed"`) {
		t.Fatalf("status must keep crashed state: code=%d body=%s", rec.Code, rec.Body.String())
	}
}
