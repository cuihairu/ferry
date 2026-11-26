package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestLandingAssignment(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mk := func(name, role string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443,
			"protocol": "vless", "config": `{"uuid":"u1"}`, "role": role,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s(%s): %d %s", name, role, rec.Code, rec.Body)
		}
		var n map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		return uint(n["id"].(float64))
	}
	entry := mk("entry-hk", "entry")
	landing := mk("landing-us", "landing")

	// validation: exactly one of entry node or region is required
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("neither scope: %d", rec.Code)
	}
	// validation: an entry node cannot serve as a landing
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": landing, "direction": "out",
	}); rec.Code == http.StatusCreated {
		// distinct entry and landing nodes with compliant roles are allowed
	} else {
		t.Fatalf("valid node-level assign: %d %s", rec.Code, rec.Body)
	}
	// validation: an entry node cannot be assigned as its own landing
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": entry, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("same node: %d", rec.Code)
	}
	// validation: illegal direction
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": landing, "direction": "sideways",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad direction: %d", rec.Code)
	}

	// region-level assignment (region set, entry empty)
	rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "香港", "landing_node_id": landing, "direction": "in", "weight": 30, "reason": "手动兜底",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("region-level assign: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["strategy"] != "manual" || created["region"] != "香港" {
		t.Fatalf("created = %v", created)
	}
	landingRowID := int(created["id"].(float64))

	// the default list returns only un-released rows (both still active)
	rec = doJSON(t, r, "GET", "/api/landings", nil)
	var rows []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("active rows = %d, want 2", len(rows))
	}

	// direction filter: only the in-bound row
	rec = doJSON(t, r, "GET", "/api/landings?direction=in", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0]["direction"] != "in" {
		t.Fatalf("direction filter = %+v", rows)
	}

	// create: bad payload 400
	if rec := doRaw(t, r, "POST", "/api/landings", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", rec.Code)
	}
	// create: weight out of range 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "hk", "landing_node_id": landing, "direction": "out", "weight": 1001,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("weight range: %d", rec.Code)
	}
	// create: missing landing node 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"region": "hk", "landing_node_id": 99999, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing landing node: %d", rec.Code)
	}
	// create: missing entry node 400
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": 99999, "landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing entry node: %d", rec.Code)
	}
	// create: non-compliant entry role 400 (a landing-role node cannot serve as entry)
	landingOnly := mk("landing-jp", "landing")
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": landingOnly, "landing_node_id": landing, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("entry role: %d", rec.Code)
	}
	// create: same node as both entry and landing 400 (a both-role node clears
	// both role checks, so the equal IDs are what catch it)
	both := mk("both-sg", "both")
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": both, "landing_node_id": both, "direction": "out",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("same node both roles: %d", rec.Code)
	}

	// adjust weight
	rec = doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"weight": 60})
	if rec.Code != http.StatusOK {
		t.Fatalf("weight update: %d %s", rec.Code, rec.Body)
	}

	// update negative paths: bad id / missing row 404, bad payload 400,
	// weight missing or out of range 400
	if rec := doJSON(t, r, "PUT", "/api/landings/abc", map[string]any{"weight": 1}); rec.Code != http.StatusNotFound {
		t.Fatalf("update bad id: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", "/api/landings/99999", map[string]any{"weight": 1}); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing row: %d", rec.Code)
	}
	if rec := doRaw(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("weight required: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"weight": 2000}); rec.Code != http.StatusBadRequest {
		t.Fatalf("update weight range: %d", rec.Code)
	}
	// release: bad id 404
	if rec := doJSON(t, r, "DELETE", "/api/landings/abc", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("release bad id: %d", rec.Code)
	}

	// release: sets released_at as a trace; releasing again gives 409; the row
	// no longer shows up in the default list
	rec = doJSON(t, r, "DELETE", fmt.Sprintf("/api/landings/%d", landingRowID), map[string]any{"reason": "换线"})
	if rec.Code != http.StatusOK {
		t.Fatalf("release: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "DELETE", fmt.Sprintf("/api/landings/%d", landingRowID), nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-release: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/landings", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("after release active rows = %d, want 1", len(rows))
	}
	// scope=all also returns the retained trace row
	rec = doJSON(t, r, "GET", "/api/landings?scope=all", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 2 {
		t.Fatalf("all rows = %d, want 2", len(rows))
	}

	_ = db
}

// TestLandingManualTTL covers the manual override TTL API surface (F-1050):
// expires_in_seconds is persisted as an absolute expires_at, and a
// non-positive TTL is rejected.
func TestLandingManualTTL(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	mk := func(name, role string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443,
			"protocol": "vless", "config": `{"uuid":"u1"}`, "role": role,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s(%s): %d %s", name, role, rec.Code, rec.Body)
		}
		var n map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		return uint(n["id"].(float64))
	}
	entry := mk("ttl-entry", "entry")
	landing := mk("ttl-landing", "landing")

	// zero/negative TTL is rejected
	if rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": landing,
		"direction": "out", "expires_in_seconds": 0,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero ttl: %d", rec.Code)
	}

	rec := doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": landing,
		"direction": "out", "expires_in_seconds": 3600,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with ttl: %d %s", rec.Code, rec.Body)
	}
	var row struct {
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.ExpiresAt == nil || *row.ExpiresAt == "" {
		t.Fatalf("expires_at missing: %s", rec.Body)
	}

	// no TTL keeps the override permanent (legacy behavior)
	rec = doJSON(t, r, "POST", "/api/landings", map[string]any{
		"entry_node_id": entry, "landing_node_id": landing, "direction": "in",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create without ttl: %d %s", rec.Code, rec.Body)
	}
	row.ExpiresAt = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.ExpiresAt != nil {
		t.Fatalf("no ttl must stay nil: %s", rec.Body)
	}
}
