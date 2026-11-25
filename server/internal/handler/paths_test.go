package handler

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathCRUD covers the Path surface (F-0105/F-0243/F-0245~F-0255):
// defaults (transport tls, direction out), node-link validation, list
// filters, merged update with version bump, multiple paths per entry and
// landing, and delete.
func TestPathCRUD(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		n := storage.Node{Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless", Token: name, Enabled: true}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry, landing, relay := mkNode("p-entry"), mkNode("p-landing"), mkNode("p-relay")

	// Defaults: transport tls, direction out, version 1.
	rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "main", "entry_node_id": entry, "landing_node_id": landing,
		"region": "us", "note": "primary route",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var p storage.Path
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.ID == 0 {
		t.Fatalf("created row = %s", rec.Body)
	}
	if p.Transport != "tls" || p.Direction != "out" || !p.Enabled || p.Version != 1 {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if p.RelayNodeID != nil {
		t.Fatalf("direct tunnel should have nil relay, got %d", *p.RelayNodeID)
	}

	// Validation failures.
	if rec = doJSON(t, r, "POST", "/api/paths", map[string]any{"name": "x", "entry_node_id": entry}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing landing = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/paths", map[string]any{"name": "x", "entry_node_id": entry, "landing_node_id": 99999}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown landing = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "x", "entry_node_id": entry, "landing_node_id": landing, "direction": "sideways",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction = %d", rec.Code)
	}

	// Multiple paths may share an entry and a landing (F-0248/F-0249).
	rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "backup", "entry_node_id": entry, "landing_node_id": landing,
		"relay_node_id": relay, "transport": "grpc", "direction": "in", "enabled": false,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second create: %d %s", rec.Code, rec.Body)
	}
	var p2 storage.Path
	json.Unmarshal(rec.Body.Bytes(), &p2)
	if p2.RelayNodeID == nil || *p2.RelayNodeID != relay || p2.Transport != "grpc" ||
		p2.Direction != "in" || p2.Enabled || p2.Version != 1 {
		t.Fatalf("second row = %+v", p2)
	}

	// Duplicate name conflicts.
	if rec = doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "main", "entry_node_id": entry, "landing_node_id": landing,
	}); rec.Code != http.StatusConflict {
		t.Fatalf("dup name = %d", rec.Code)
	}

	// Update merges absent fields and bumps the config version (F-0252).
	rec = doJSON(t, r, "PUT", "/api/paths/"+utoa(p.ID), map[string]any{
		"relay_node_id": relay, "enabled": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	var up storage.Path
	json.Unmarshal(rec.Body.Bytes(), &up)
	if up.Name != "main" || up.EntryNodeID != entry || up.LandingNodeID != landing ||
		up.Region != "us" || up.Note != "primary route" || up.Transport != "tls" || up.Direction != "out" {
		t.Fatalf("absent fields not preserved: %+v", up)
	}
	if up.RelayNodeID == nil || *up.RelayNodeID != relay || up.Version != 2 {
		t.Fatalf("relay/version not applied: %+v", up)
	}

	// List + filters (F-0255).
	rec = doJSON(t, r, "GET", "/api/paths?entry_node_id="+utoa(entry), nil)
	if rec.Code != http.StatusOK || len(mustJSON(t, rec)["paths"].([]any)) != 2 {
		t.Fatalf("entry filter: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/paths?direction=in", nil)
	if paths := mustJSON(t, rec)["paths"].([]any); len(paths) != 1 {
		t.Fatalf("direction filter = %s", rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/paths?landing_node_id="+utoa(landing)+"&enabled=false", nil)
	if paths := mustJSON(t, rec)["paths"].([]any); len(paths) != 1 {
		t.Fatalf("landing+enabled filter = %s", rec.Body)
	}
	if rec = doJSON(t, r, "GET", "/api/paths/"+utoa(p.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/paths/999", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing get = %d", rec.Code)
	}

	// Delete: second delete 404s.
	if rec = doJSON(t, r, "DELETE", "/api/paths/"+utoa(p2.ID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec = doJSON(t, r, "DELETE", "/api/paths/"+utoa(p2.ID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete = %d", rec.Code)
	}
}

// TestPathDeleteDependencyCheck covers F-0254: a path that is a group member
// is refused until it is removed from the group.
func TestPathDeleteDependencyCheck(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		n := storage.Node{Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless", Token: name, Enabled: true}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry, landing := mkNode("dep-entry"), mkNode("dep-landing")

	rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "dep-path", "entry_node_id": entry, "landing_node_id": landing,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create path: %d %s", rec.Code, rec.Body)
	}
	pathID := uint(mustJSON(t, rec)["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/path-groups", map[string]any{"name": "dep-group"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	groupID := uint(mustJSON(t, rec)["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/path-groups/"+utoa(groupID)+"/members", map[string]any{"path_id": pathID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add member: %d %s", rec.Code, rec.Body)
	}

	// Referenced path is not deletable (F-0254).
	if rec = doJSON(t, r, "DELETE", "/api/paths/"+utoa(pathID), nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete referenced path = %d %s", rec.Code, rec.Body)
	}
	// Remove from the group, then deletion succeeds.
	if rec = doJSON(t, r, "DELETE", "/api/path-groups/"+utoa(groupID)+"/members/"+utoa(pathID), nil); rec.Code != http.StatusOK {
		t.Fatalf("remove member: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", "/api/paths/"+utoa(pathID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete after unreference: %d %s", rec.Code, rec.Body)
	}
}
