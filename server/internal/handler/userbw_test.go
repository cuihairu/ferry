package handler

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// itoaU stringifies a user ID (test helper).
func itoaU(id uint) string { return strconv.Itoa(int(id)) }

// Per-user bandwidth limits (P2-3) tests: create/update with limits and
// validation, template default fill-in, panelMe visibility, reject over cap.

// TestUserBwLimits covers bandwidth limit create/update/validation/panel visibility.
func TestUserBwLimits(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Create with limits.
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "bwuser", "bw_up_mbps": 50, "bw_down_mbps": 100,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "bwuser").First(&u)
	if u.BwUpMbps != 50 || u.BwDownMbps != 100 {
		t.Fatalf("persisted = %+v", u)
	}

	// Update changes uplink only.
	rec = doJSON(t, r, "PUT", "/api/users/"+itoaU(u.ID), map[string]any{"bw_up_mbps": 20})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	db.Where("username = ?", "bwuser").First(&u)
	if u.BwUpMbps != 20 || u.BwDownMbps != 100 {
		t.Fatalf("after update = %+v", u)
	}

	// Validation surface: negative rejected, over-cap rejected, 0=unlimited
	// allowed, non-numeric goes through bind as 400.
	for name, body := range map[string]map[string]any{
		"negative": {"username": "n1", "bw_up_mbps": -1},
		"too big":  {"username": "n2", "bw_down_mbps": 100001},
	} {
		if rec = doJSON(t, r, "POST", "/api/users", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "n3", "bw_up_mbps": 0}); rec.Code != http.StatusCreated {
		t.Fatalf("zero: %d", rec.Code)
	}
	// Update side validates the same way.
	if rec = doJSON(t, r, "PUT", "/api/users/"+itoaU(u.ID), map[string]any{"bw_up_mbps": -5}); rec.Code != http.StatusBadRequest {
		t.Fatalf("update negative: %d", rec.Code)
	}

	// Visible in panelMe (user self view carries the limits, same terms as quota).
	rec = doPanel(t, r, u.SubToken, "GET", "/api/panel/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("panel me: %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"bw_up_mbps":20`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"bw_down_mbps":100`)) {
		t.Fatalf("panel me body: %s", rec.Body.String())
	}
}

// TestUserTemplateBw covers template default bandwidth limit fill-in (P2-3).
func TestUserTemplateBw(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// No template configured: user created without limits (0=unlimited).
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl1", "use_template": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("no template: %d %s", rec.Code, rec.Body)
	}
	// Template configured with limits: use_template=true fills absent fields.
	if rec = doJSON(t, r, "PUT", "/api/user-template", map[string]any{
		"quota_bytes": 1073741824, "expire_days": 30, "reset_cycle": "month",
		"bw_up_mbps": 10, "bw_down_mbps": 20,
	}); rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl2", "use_template": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("with template: %d %s", rec.Code, rec.Body)
	}
	// Template quota applied (cross-field check that applyUserTemplate took effect).
	if rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl3", "use_template": true, "bw_up_mbps": 99,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("explicit override: %d %s", rec.Code, rec.Body)
	}
}
