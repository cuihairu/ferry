package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func subAccessRows(t *testing.T, db *gorm.DB) []storage.SubAccessLog {
	t.Helper()
	var rows []storage.SubAccessLog
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// F-1150: a delivered subscription lands one audit row with the resolved user,
// the route path, the negotiated target and the response code; a conditional-get
// hit records the 304 the same way.
func TestSubAccessLogRecordsDelivery(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	tok := panelUserOf(t, r, "audit-hit", nil)
	if err := db.Create(&storage.Node{Name: "hk", Address: "a.example.com", Port: 443,
		Protocol: "vless", Config: `{"uuid":"u"}`, Enabled: true,
		Token: "tok-audit", Role: "entry", Status: "online"}).Error; err != nil {
		t.Fatal(err)
	}

	rec := getSub(t, r, "/sub/"+tok, "Clash/v1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("fetch: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", "audit-hit").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	rows := subAccessRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.UserID != u.ID || row.Path != "/sub/:token" || row.Target != "clash" || row.Code != 200 {
		t.Fatalf("row = %+v (user %d)", row, u.ID)
	}
	if row.IP == "" || row.UA != "Clash/v1.0" {
		t.Fatalf("row context missing: %+v", row)
	}

	// Conditional-get hit: the 304 short-circuit is still a delivery attempt.
	req := httptest.NewRequest(http.MethodGet, "/sub/"+tok, nil)
	req.Header.Set("User-Agent", "Clash/v1.0")
	req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	rec304 := httptest.NewRecorder()
	r.ServeHTTP(rec304, req)
	if rec304.Code != http.StatusNotModified {
		t.Fatalf("conditional get: %d", rec304.Code)
	}
	rows = subAccessRows(t, db)
	if len(rows) != 2 || rows[1].Code != 304 || rows[1].UserID != u.ID {
		t.Fatalf("304 rows = %+v", rows)
	}
}

// F-1150: misses are recorded too (anti-enumeration forensics) -- an unknown
// token stays user_id 0 with the 404 code. The raw token never lands anywhere:
// the row has no token column at all.
func TestSubAccessLogRecordsMiss(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	if rec := getSub(t, r, "/sub/no-such-token", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("miss: %d", rec.Code)
	}
	rows := subAccessRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].UserID != 0 || rows[0].Code != 404 || rows[0].Target != "v2ray" {
		t.Fatalf("miss row = %+v", rows[0])
	}
}

// F-1150: the custom subscription surface records with its own route path (and
// its own token), and an explicit ?target= overrides the UA negotiation in the
// audit row.
func TestSubAccessLogCustomSurface(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	alice := panelUserOf(t, r, "audit-x", nil)
	seedEntryNodes(t, db)
	subTok := createSubX(t, r, alice, "audit-x-sub", "", "")

	rec := getSub(t, r, "/sub/x/"+subTok+"?target=singbox", "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("custom fetch: %d %s", rec.Code, rec.Body)
	}
	rows := subAccessRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	if rows[0].Path != "/sub/x/:token" || rows[0].Target != "singbox" || rows[0].Code != 200 {
		t.Fatalf("custom row = %+v", rows[0])
	}
}
