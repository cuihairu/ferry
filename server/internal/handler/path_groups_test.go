package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathGroupMembers covers F-0261~F-0267: group CRUD with filters, ordered
// membership, default path selection, manual exclusion, and direction checks.
func TestPathGroupMembers(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		n := storage.Node{Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless", Token: name, Enabled: true}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry, landing := mkNode("g-entry"), mkNode("g-landing")

	mkPath := func(name, direction, region string) uint {
		rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
			"name": name, "entry_node_id": entry, "landing_node_id": landing,
			"direction": direction, "region": region,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create path %s: %d %s", name, rec.Code, rec.Body)
		}
		return uint(mustJSON(t, rec)["id"].(float64))
	}
	out1, out2, in1 := mkPath("g-out-1", "out", "us"), mkPath("g-out-2", "out", "us"), mkPath("g-in-1", "in", "us")

	// Defaults (direction out) + group filters (F-0266).
	rec := doJSON(t, r, "POST", "/api/path-groups", map[string]any{
		"name": "us-out", "region": "us", "purpose": "streaming", "note": "primary group",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	groupID := uint(mustJSON(t, rec)["id"].(float64))
	rec = doJSON(t, r, "GET", "/api/path-groups?direction=out&region=us&purpose=streaming", nil)
	if groups := mustJSON(t, rec)["path_groups"].([]any); len(groups) != 1 {
		t.Fatalf("group filters = %s", rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/path-groups", map[string]any{"name": "us-out"}); rec.Code != http.StatusConflict {
		t.Fatalf("dup group name = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/path-groups", map[string]any{"name": "bad", "direction": "sideways"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction = %d", rec.Code)
	}

	// Add members with explicit positions; direction mismatch is rejected (F-0266).
	add := func(pathID uint, position int) {
		rec := doJSON(t, r, "POST", "/api/path-groups/"+utoa(groupID)+"/members", map[string]any{
			"path_id": pathID, "position": position,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("add member %d: %d %s", pathID, rec.Code, rec.Body)
		}
	}
	add(out2, 20)
	add(out1, 10)
	if rec = doJSON(t, r, "POST", "/api/path-groups/"+utoa(groupID)+"/members", map[string]any{"path_id": in1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("direction mismatch = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/path-groups/"+utoa(groupID)+"/members", map[string]any{"path_id": out1}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate member = %d", rec.Code)
	}

	// Get returns ordered members (F-0265).
	rec = doJSON(t, r, "GET", "/api/path-groups/"+utoa(groupID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get group: %d %s", rec.Code, rec.Body)
	}
	members := mustJSON(t, rec)["members"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["path_id"].(float64) != float64(out1) {
		t.Fatalf("member order = %s", rec.Body)
	}

	// Default path must be a member (F-0264).
	if rec = doJSON(t, r, "PUT", "/api/path-groups/"+utoa(groupID), map[string]any{"default_path_id": in1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("default non-member = %d", rec.Code)
	}
	rec = doJSON(t, r, "PUT", "/api/path-groups/"+utoa(groupID), map[string]any{"default_path_id": out1})
	if rec.Code != http.StatusOK || mustJSON(t, rec)["default_path_id"].(float64) != float64(out1) {
		t.Fatalf("set default: %d %s", rec.Code, rec.Body)
	}

	// Reposition + exclude (F-0265/F-0267).
	rec = doJSON(t, r, "PATCH", "/api/path-groups/"+utoa(groupID)+"/members/"+utoa(out2), map[string]any{
		"position": 5, "excluded": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update member: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/path-groups/"+utoa(groupID), nil)
	members = mustJSON(t, rec)["members"].([]any)
	if len(members) != 2 || members[0].(map[string]any)["path_id"].(float64) != float64(out2) ||
		members[0].(map[string]any)["excluded"] != true {
		t.Fatalf("member after update = %s", rec.Body)
	}

	// Removing the default clears the group default (F-0264).
	if rec = doJSON(t, r, "DELETE", "/api/path-groups/"+utoa(groupID)+"/members/"+utoa(out1), nil); rec.Code != http.StatusOK {
		t.Fatalf("remove default member: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/path-groups/"+utoa(groupID), nil)
	if got := mustJSON(t, rec)["path_group"].(map[string]any)["default_path_id"]; got != nil {
		t.Fatalf("default after member removal = %v", got)
	}

	// Group delete removes remaining members.
	if rec = doJSON(t, r, "DELETE", "/api/path-groups/"+utoa(groupID), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete group: %d %s", rec.Code, rec.Body)
	}
	var n int64
	db.Model(&storage.PathGroupMember{}).Where("group_id = ?", groupID).Count(&n)
	if n != 0 {
		t.Fatalf("group members survived delete: %d", n)
	}
	if rec = doJSON(t, r, "GET", "/api/path-groups/"+utoa(groupID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted group = %d", rec.Code)
	}
}
