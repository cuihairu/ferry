package handler

import (
	"net/http"
	"testing"
)

// TestNodeBusinessMetaEdit covers the F-0743 surface: labels/group and the cost
// fields are creatable and editable, keep-on-absent, and validated.
func TestNodeBusinessMetaEdit(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, http.MethodPost, "/api/nodes", map[string]any{
		"name": "bm-1", "address": "bm.example.com", "port": 443, "protocol": "vless",
		"labels": []string{"edge", "cheap"}, "group": "cn-east",
		"billing_type": "monthly", "monthly_cost_cents": 3100, "currency": "CNY",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := utoa(uint(mustJSON(t, rec)["id"].(float64)))

	// Update: replace labels, edit the price, add a cost note; fields left out keep.
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{
		"labels": []string{"edge", " edge2 ", "edge"}, "monthly_cost_cents": 6200, "cost_note": "promo",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	node := mustJSON(t, rec)
	if node["labels"] != `["edge","edge2"]` {
		t.Fatalf("labels must trim and dedupe: %v", node["labels"])
	}
	if node["monthly_cost_cents"] != float64(6200) {
		t.Fatalf("monthly cost must update: %v", node["monthly_cost_cents"])
	}
	if node["group"] != "cn-east" || node["billing_type"] != "monthly" {
		t.Fatalf("left-out fields must keep: %v", node)
	}

	// Explicit 0 clears the price (pointer semantics).
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{"monthly_cost_cents": 0})
	if rec.Code != http.StatusOK || mustJSON(t, rec)["monthly_cost_cents"] != float64(0) {
		t.Fatalf("explicit zero must clear: %d %s", rec.Code, rec.Body)
	}

	// Validation: unknown billing type, negative price, empty label entry.
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{"billing_type": "per-carrier-pigeon"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad billing type must fail: %d", rec.Code)
	}
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{"traffic_price_cents": -1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative price must fail: %d", rec.Code)
	}
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{"labels": []string{"ok", " "}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty label entry must fail: %d", rec.Code)
	}

	// An empty labels list is explicit and clears to [].
	rec = doJSON(t, r, http.MethodPut, "/api/nodes/"+id, map[string]any{"labels": []string{}})
	if rec.Code != http.StatusOK || mustJSON(t, rec)["labels"] != "[]" {
		t.Fatalf("empty list must clear labels: %d %s", rec.Code, rec.Body)
	}
}
