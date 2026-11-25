package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestEntryDomainsCRUD covers the entry-domain data plane (TOUCH-3): create
// (protocol-prefix stripping, role validation), update (partial update
// including disable), list, delete, and 404.
func TestEntryDomainsCRUD(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// invalid role 400, empty domain 400
	if rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "a.com", "role": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": " ", "role": "primary"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty domain: %d", rec.Code)
	}

	// create: https prefix and trailing slash stripped
	rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "https://panel.example.com/", "role": "primary", "region": "cn"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "backup.example.org", "role": "backup"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create backup: %d %s", rec.Code, rec.Body)
	}

	// list: two rows in ascending id order
	rec = doJSON(t, r, "GET", "/api/entry-domains", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rows) != 2 || rows[0]["domain"] != "panel.example.com" || rows[0]["role"] != "primary" || rows[1]["role"] != "backup" {
		t.Fatalf("unexpected rows: %+v", rows)
	}

	// update: disable + change role
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"enabled": false, "role": "backup"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/entry-domains", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if rows[0]["enabled"] != false || rows[0]["role"] != "backup" {
		t.Fatalf("update leaked: %+v", rows[0])
	}
	// invalid role update 400
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"role": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad update role: %d", rec.Code)
	}
	// bad payload 400 (create and update)
	if rec := doRaw(t, r, "POST", "/api/entry-domains", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad json: %d", rec.Code)
	}
	if rec := doRaw(t, r, "PUT", "/api/entry-domains/1", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad json: %d", rec.Code)
	}
	// update: blanked-out domain 400
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"domain": "  "}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty update domain: %d", rec.Code)
	}
	// update: domain normalization (strip scheme and trailing slash) and region trimmed of surrounding whitespace
	rec = doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"domain": "https://new.example.com/", "region": " us "})
	if rec.Code != http.StatusOK {
		t.Fatalf("update domain: %d %s", rec.Code, rec.Body)
	}
	var one map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if one["domain"] != "new.example.com" || one["region"] != "us" {
		t.Fatalf("normalized row = %+v", one)
	}

	// delete: one row left after delete, re-delete / re-update 404
	if rec := doJSON(t, r, "DELETE", "/api/entry-domains/2", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := doJSON(t, r, "DELETE", "/api/entry-domains/2", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/2", map[string]any{"enabled": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/entry-domains", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 1 {
		t.Fatalf("rows after delete: %d", len(rows))
	}
}
