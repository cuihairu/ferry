package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPolicyAPI covers F-0282~F-0287 + F-0295/F-0296 over the API: scoped
// rows with validation, one per scope+subject, version bump on update, the
// resolved view, and the simulate/explain endpoint.
func TestPolicyAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Validation: bad scope, bad constraint value, unknown subject.
	if rec := doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "galaxy", "constraints": map[string]any{},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scope = %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "user",
		"subject_id":  1,
		"constraints": map[string]any{"min_health": "immortal"},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad min_health = %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "user", "constraints": map[string]any{},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing subject = %d %s", rec.Code, rec.Body)
	}

	// Create a target user, then the system policy and the user policy.
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "poluser"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	uid := uint(mustJSON(t, rec)["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "system",
		"constraints": map[string]any{"regions": []string{"HK"}, "failover": "auto"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create system policy: %d %s", rec.Code, rec.Body)
	}
	sysID := uint(mustJSON(t, rec)["id"].(float64))

	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "user", "subject_id": uid,
		"constraints": map[string]any{"regions": []string{"JP"}, "min_health": "healthy"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user policy: %d %s", rec.Code, rec.Body)
	}
	userID := uint(mustJSON(t, rec)["id"].(float64))

	// One policy per scope+subject.
	if rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope": "system", "constraints": map[string]any{},
	}); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate policy = %d %s", rec.Code, rec.Body)
	}

	// Update bumps the version and records the editor (F-0295).
	if rec = doJSON(t, r, "PUT", "/api/policies/"+utoa(userID), map[string]any{
		"constraints": map[string]any{"regions": []string{"JP"}, "output": "compact"},
	}); rec.Code != http.StatusOK {
		t.Fatalf("update policy: %d %s", rec.Code, rec.Body)
	}
	updated := mustJSON(t, rec)
	if updated["version"].(float64) != 2 {
		t.Fatalf("version = %v", updated["version"])
	}
	if updated["updated_by"] == "" {
		t.Fatal("updated_by missing")
	}

	// Resolved view (F-0287): the user's regions/output override the system's;
	// the system's failover (untouched by the user) inherits.
	rec = doJSON(t, r, "GET", "/api/policies/resolve?user_id="+utoa(uid), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	body := mustJSON(t, rec)
	eff := body["constraints"].(map[string]any)
	if eff["regions"].([]any)[0] != "JP" || eff["output"] != "compact" || eff["failover"] != "auto" {
		t.Fatalf("resolved constraints = %v", eff)
	}
	if rows := body["policies"].([]any); len(rows) != 2 {
		t.Fatalf("contributing policies = %d", len(rows))
	}

	// Simulate (F-0296): an HK node passes, a US node is explained away.
	if err := db.Create(&storage.Node{
		Name: "pol-hk", Token: "pol-hk", Role: "entry", Enabled: true,
		Status: "online", PoolState: "active", Region: "JP", ISP: "HKT",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Node{
		Name: "pol-us", Token: "pol-us", Role: "entry", Enabled: true,
		Status: "online", PoolState: "active", Region: "US", ISP: "Cogent",
	}).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/policies/simulate?user_id="+utoa(uid), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("simulate: %d %s", rec.Code, rec.Body)
	}
	body = mustJSON(t, rec)
	kept := body["kept"].([]any)
	keptNames := map[string]bool{}
	for _, k := range kept {
		keptNames[k.(map[string]any)["name"].(string)] = true
	}
	if !keptNames["pol-hk"] || keptNames["pol-us"] {
		t.Fatalf("simulate kept = %v", keptNames)
	}
	dropped := body["dropped"].([]any)
	if len(dropped) != 1 || dropped[0].(map[string]any)["reason"] != "region not allowed" {
		t.Fatalf("simulate dropped = %v", dropped)
	}
	if body["failover_free"] != false {
		t.Fatalf("failover_free = %v", body["failover_free"])
	}

	// Delete cleans up.
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(sysID), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete policy: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", "/api/policies/"+utoa(sysID), nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
	var left int64
	db.Model(&storage.Policy{}).Where("scope = ?", "system").Count(&left)
	if left != 0 {
		t.Fatalf("system policy survived delete: %d", left)
	}
}

// TestPolicyGroupLink covers F-0270's API surface: the path_group_id pin is
// validated against existing groups, rides the resolve view, and the
// simulate/explain endpoint shows which member the group would serve.
func TestPolicyGroupLink(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Unknown group reference: rejected.
	if rec := doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "system",
		"constraints": map[string]any{"path_group_id": 99999},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown group = %d %s", rec.Code, rec.Body)
	}

	// One entry path in one group, as the group default.
	entry := storage.Node{Name: "grpg-entry", Token: "grpg-entry", Address: "grpg.example.com",
		Port: 443, Role: "entry", Enabled: true, Status: "online", PoolState: "active"}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	landing := storage.Node{Name: "grpg-land", Token: "grpg-land", Port: 443,
		Role: "landing", Enabled: true, Status: "online", PoolState: "active"}
	if err := db.Create(&landing).Error; err != nil {
		t.Fatal(err)
	}
	path := storage.Path{Name: "grpg-path", EntryNodeID: entry.ID, LandingNodeID: landing.ID}
	if err := db.Create(&path).Error; err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, r, "POST", "/api/path-groups", map[string]any{"name": "grpg", "direction": "out"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create group: %d %s", rec.Code, rec.Body)
	}
	gid := uint(mustJSON(t, rec)["id"].(float64))
	rec = doJSON(t, r, "POST", "/api/path-groups/"+utoa(gid)+"/members", map[string]any{"path_id": path.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add member: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "PUT", "/api/path-groups/"+utoa(gid), map[string]any{"default_path_id": path.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("set default: %d %s", rec.Code, rec.Body)
	}

	// Valid reference: accepted, rides the resolve view.
	rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "system",
		"constraints": map[string]any{"path_group_id": gid},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create policy: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/policies/resolve", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	if got := mustJSON(t, rec)["constraints"].(map[string]any)["path_group_id"].(float64); got != float64(gid) {
		t.Fatalf("resolved path_group_id = %v want %d", got, gid)
	}

	// Simulate explains the current serving member (no probes: unknown).
	rec = doJSON(t, r, "GET", "/api/policies/simulate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("simulate: %d %s", rec.Code, rec.Body)
	}
	body := mustJSON(t, rec)
	group := body["path_group"].(map[string]any)
	if group["group_id"].(float64) != float64(gid) || group["path_id"].(float64) != float64(path.ID) ||
		group["entry_node_id"].(float64) != float64(entry.ID) || group["health"] != "unknown" {
		t.Fatalf("simulate path_group = %v", group)
	}

	// Deleting the group leaves the policy valid but unresolvable, and the
	// validation refuses new references to it.
	if rec = doJSON(t, r, "DELETE", "/api/path-groups/"+utoa(gid), nil); rec.Code != http.StatusOK {
		t.Fatalf("delete group: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/policies/simulate", nil)
	if rec.Code != http.StatusOK || mustJSON(t, rec)["path_group"] != nil {
		t.Fatalf("simulate after group delete = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/policies", map[string]any{
		"scope":       "system",
		"constraints": map[string]any{"path_group_id": gid},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("dangling group reference = %d %s", rec.Code, rec.Body)
	}
}
