package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestDistributorLoginAuditRows covers F-0045 for the unauthenticated distributor
// surface: every login attempt (success, bad password, unknown user, disabled
// account) lands one audit row with the submitted username and the real outcome.
func TestDistributorLoginAuditRows(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{"username": "d-audit", "password": "secret123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := uint(mustJSON(t, rec)["distributor"].(map[string]any)["id"].(float64))

	for _, tc := range []struct {
		username, password string
		status             int
	}{
		{"d-audit", "secret123", http.StatusOK},
		{"d-audit", "wrong", http.StatusUnauthorized},
		{"ghost", "x", http.StatusUnauthorized},
	} {
		rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": tc.username, "password": tc.password})
		if rec.Code != tc.status {
			t.Fatalf("login %s: %d %s", tc.username, rec.Code, rec.Body)
		}
	}

	var rows []storage.AuditLog
	if err := db.Where("action = ?", "POST /distributor/login").Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("audit rows = %d, want 3", len(rows))
	}
	if rows[0].Actor != "d-audit" || rows[0].ActorKind != "distributor" || !rows[0].Success {
		t.Fatalf("success row = %+v", rows[0])
	}
	if rows[1].Actor != "d-audit" || rows[1].Success {
		t.Fatalf("bad-password row = %+v", rows[1])
	}
	if rows[2].Actor != "ghost" || rows[2].Success {
		t.Fatalf("unknown-user row = %+v", rows[2])
	}
	// Password never reaches the audit body snapshot.
	for _, row := range rows {
		if strings.Contains(row.Body, "secret123") || strings.Contains(row.Body, "wrong") {
			t.Fatalf("audit row leaked the password: %+v", row)
		}
	}

	// Disabled account also lands an audit row with failure recorded.
	rec = doJSON(t, r, "PUT", "/api/distributors/"+utoa(id), map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/distributor/login", map[string]any{"username": "d-audit", "password": "secret123"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled login: %d %s", rec.Code, rec.Body)
	}
	var last storage.AuditLog
	if err := db.Where("action = ?", "POST /distributor/login").Order("id DESC").First(&last).Error; err != nil {
		t.Fatal(err)
	}
	if last.Success || last.Actor != "d-audit" {
		t.Fatalf("disabled row = %+v", last)
	}
}

// TestPanelRedeemAuditRows covers F-0045 for the panel surface: a redemption
// writes the subscription user and card code into the audit trail with its
// outcome, even though this route sits outside auditMiddleware.
func TestPanelRedeemAuditRows(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	userID, codes := redeemCreate(t, r, "redeem-audit", map[string]any{
		"grant_type": "add_quota", "grant_value": 2 << 30,
	}, 2)
	var u storage.User
	if err := db.First(&u, userID).Error; err != nil {
		t.Fatal(err)
	}

	rec := doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]})
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body)
	}
	// Reuse fails; both attempts must be audited with target = the card code.
	rec = doPanel(t, r, u.SubToken, "POST", "/api/panel/redeem", map[string]any{"code": codes[0]})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reuse: %d %s", rec.Code, rec.Body)
	}

	var rows []storage.AuditLog
	if err := db.Where("action = ?", "POST /api/panel/redeem").Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(rows))
	}
	if rows[0].Actor != u.Username || rows[0].ActorKind != "panel" || rows[0].Target != codes[0] || !rows[0].Success {
		t.Fatalf("success row = %+v", rows[0])
	}
	if rows[1].Actor != u.Username || rows[1].Target != codes[0] || rows[1].Success {
		t.Fatalf("failure row = %+v", rows[1])
	}

	// An unauthenticated request is aborted before the audited section: no false
	// success entry is written.
	rec = doPanel(t, r, "", "POST", "/api/panel/redeem", map[string]any{"code": codes[1]})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unauthenticated redeem: %d %s", rec.Code, rec.Body)
	}
	var n int64
	if err := db.Model(&storage.AuditLog{}).Where("action = ?", "POST /api/panel/redeem").Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("audit rows after unauthenticated request = %d, want 2", n)
	}
}
