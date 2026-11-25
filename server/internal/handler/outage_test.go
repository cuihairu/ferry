package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestOutage covers outage resilience (TOUCH-7): the outage flag defaults to
// normal, missing enabled 400, subscription notes always carrying the backup
// announcement address and the enabled domain list (primary/backup marked,
// disabled ones never leaked), the v2ray decoded text and the clash YAML notes
// both picking up the warning line once outage mode is on, and it clearing on
// turn-off.
func TestOutage(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Normal by default
	rec := doJSON(t, r, "GET", "/api/outage", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"enabled":false}` {
		t.Fatalf("default outage: %d %s", rec.Code, rec.Body)
	}
	// Missing enabled 400
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: %d", rec.Code)
	}

	// Backup domains seeded: one each of primary/spare/disabled (the disabled one
	// must not enter the notes)
	for _, d := range []storage.EntryDomain{
		{Domain: "main.example.com", Role: "primary", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "spare.example.com", Role: "backup", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "dead.example.com", Role: "backup", Enabled: false, UpdatedAt: time.Now()},
	} {
		if err := db.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Subscription user (no nodes is fine: the notes are still attached, since the
	// escape hatch must reach the client cache before the outage)
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "outage-user"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	tok := u["sub_token"].(string)

	// Normal state: the notes carry the announcement address and the domain list,
	// no warning
	raw, err := base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil {
		t.Fatalf("v2ray body not base64: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "公告订阅") || !strings.Contains(text, "/feed.xml") {
		t.Fatalf("feed note missing: %q", text)
	}
	if !strings.Contains(text, "main.example.com(主)") || !strings.Contains(text, "spare.example.com(备)") {
		t.Fatalf("domain note missing: %q", text)
	}
	if strings.Contains(text, "dead.example.com") || strings.Contains(text, "警告") {
		t.Fatalf("disabled domain leaked or premature warning: %q", text)
	}
	clashBody := getSub(t, r, "/sub/"+tok+"?target=clash", "curl/8.0").Body.String()
	if !strings.Contains(clashBody, "# 公告订阅") || !strings.Contains(clashBody, "# 备用入口域名") {
		t.Fatalf("clash notes missing:\n%s", clashBody)
	}

	// Outage mode on: the warning line enters both subscription texts
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{"enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("put outage: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/outage", nil); rec.Body.String() != `{"enabled":true}` {
		t.Fatalf("outage not on: %s", rec.Body)
	}
	raw, err = base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil || !strings.Contains(string(raw), "警告：面板入口处于断联态") {
		t.Fatalf("v2ray warning missing: %v %q", err, string(raw))
	}
	clashBody = getSub(t, r, "/sub/"+tok+"?target=clash", "curl/8.0").Body.String()
	if !strings.Contains(clashBody, "# 警告：面板入口处于断联态") {
		t.Fatalf("clash warning missing:\n%s", clashBody)
	}

	// Turning it off clears it right away
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("put outage off: %d", rec.Code)
	}
	raw, err = base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil || strings.Contains(string(raw), "警告") {
		t.Fatalf("warning should clear: %v %q", err, string(raw))
	}
}
