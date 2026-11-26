package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSetHealthOverride covers PUT/GET/DELETE /api/nodes/:id/health_override
// (F-0932/F-0933): a valid override persists with reason/operator/expiry, a
// second PUT replaces it in place, DELETE clears it and 404s afterwards.
func TestSetHealthOverride(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	rec := doJSON(t, r, "PUT", "/api/nodes/1/health_override", map[string]any{
		"state": "healthy", "reason": "probe agent redeploy", "operator": "oncall",
		"expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set override: %d %s", rec.Code, rec.Body)
	}
	var row storage.HealthOverride
	if err := json.Unmarshal(rec.Body.Bytes(), &row); err != nil {
		t.Fatalf("response: %v", err)
	}
	if row.NodeID != 1 || row.State != "healthy" || row.Reason != "probe agent redeploy" ||
		row.Operator != "oncall" || row.ExpiresAt.IsZero() {
		t.Fatalf("stored override = %+v", row)
	}

	// GET returns the row.
	rec = doJSON(t, r, "GET", "/api/nodes/1/health_override", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get override: %d %s", rec.Code, rec.Body)
	}

	// A second PUT replaces in place: still one row, new reason/operator.
	rec = doJSON(t, r, "PUT", "/api/nodes/1/health_override", map[string]any{
		"state": "degraded", "reason": "suspected flapping", "operator": "neteng",
		"expires_at": "2030-06-01T00:00:00Z",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("replace override: %d %s", rec.Code, rec.Body)
	}
	var rows []storage.HealthOverride
	if err := db.Where("node_id = ?", 1).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "degraded" || rows[0].Operator != "neteng" {
		t.Fatalf("after replace rows = %+v; want one degraded by neteng", rows)
	}

	// DELETE clears it; a second DELETE 404s.
	rec = doJSON(t, r, "DELETE", "/api/nodes/1/health_override", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete override: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/nodes/1/health_override", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/nodes/1/health_override", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d %s", rec.Code, rec.Body)
	}
}

// TestSetHealthOverrideValidation pins the F-0933 contract: an override
// without a reason, an operator or a future expiry is rejected, and unknown
// nodes 404.
func TestSetHealthOverrideValidation(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"unknown state", map[string]any{
			"state": "unknown", "reason": "x", "operator": "y",
			"expires_at": "2030-01-01T00:00:00Z"}, "state must be healthy/degraded"},
		{"off is not forciable", map[string]any{
			"state": "off", "reason": "x", "operator": "y",
			"expires_at": "2030-01-01T00:00:00Z"}, "state must be healthy/degraded"},
		{"empty reason", map[string]any{
			"state": "healthy", "reason": "  ", "operator": "y",
			"expires_at": "2030-01-01T00:00:00Z"}, "reason is required"},
		{"empty operator", map[string]any{
			"state": "healthy", "reason": "x", "operator": "",
			"expires_at": "2030-01-01T00:00:00Z"}, "operator is required"},
		{"past expiry", map[string]any{
			"state": "healthy", "reason": "x", "operator": "y",
			"expires_at": "2020-01-01T00:00:00Z"}, "expires_at"},
		{"missing expiry", map[string]any{
			"state": "healthy", "reason": "x", "operator": "y"}, "expires_at"},
	}
	for _, tc := range cases {
		rec := doJSON(t, r, "PUT", "/api/nodes/1/health_override", tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s: %d %s; want 400 with %q", tc.name, rec.Code, rec.Body, tc.want)
		}
	}

	rec := doJSON(t, r, "PUT", "/api/nodes/999/health_override", map[string]any{
		"state": "healthy", "reason": "x", "operator": "y",
		"expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node: %d %s", rec.Code, rec.Body)
	}
}

// TestOverrideShowsInNodeStatus is the F-0932 end-to-end check: the forced
// state is what the node status API reports, and clearing it lets the
// computed state (unknown without verdicts) resume.
func TestOverrideShowsInNodeStatus(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	rec := doJSON(t, r, "GET", "/api/nodes/1/status", nil)
	if rec.Code != http.StatusOK || mustJSON(t, rec)["health"] != "unknown" {
		t.Fatalf("baseline health = %s", rec.Body)
	}

	rec = doJSON(t, r, "PUT", "/api/nodes/1/health_override", map[string]any{
		"state": "degraded", "reason": "maintenance window", "operator": "oncall",
		"expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("set override: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/nodes/1/status", nil)
	body := mustJSON(t, rec)
	if body["health"] != "degraded" {
		t.Fatalf("overridden health = %v; want degraded", body["health"])
	}
	// F-0934: the reason travels with the state and names the override.
	if reason, _ := body["health_reason"].(string); !strings.Contains(reason, "manual override by oncall") {
		t.Fatalf("health_reason = %q; want the override audit", reason)
	}

	rec = doJSON(t, r, "DELETE", "/api/nodes/1/health_override", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear override: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/nodes/1/status", nil)
	if got := mustJSON(t, rec)["health"]; got != "unknown" {
		t.Fatalf("health after clear = %v; want unknown", got)
	}
}
