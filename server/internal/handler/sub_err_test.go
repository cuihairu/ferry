package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// subFixture starts a router and creates a valid subscription user (quota 1000,
// expiring in 2030), returning the router, the db and the subscription token.
func subFixture(t *testing.T) (*gin.Engine, *gorm.DB, string) {
	t.Helper()
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "sub-u", "quota_bytes": 1000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u struct {
		SubToken string `json:"sub_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.SubToken == "" {
		t.Fatal("user has no sub_token")
	}
	return r, db, u.SubToken
}

// TestSubUserErrors covers subscription-chain user query failures and disabled users being treated as 404.
func TestSubUserErrors(t *testing.T) {
	// Unknown token -> 404 (anti-enumeration; only the failure-count path, no other routes).
	r, _, _ := subFixture(t)
	if rec := getSub(t, r, "/sub/no-such-token", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// User query db failure (not a record-miss) -> 500.
	r2, db2, tok2 := subFixture(t)
	regQueryFail("t:subuser", db2, (*storage.User)(nil))
	if rec := getSub(t, r2, "/sub/"+tok2, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("user query fail: %d %s", rec.Code, rec.Body)
	}

	// Disabled user -> 404 (the token counts as revoked).
	r3, db3, tok3 := subFixture(t)
	if err := db3.Model(&storage.User{}).Where("sub_token = ?", tok3).
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getSub(t, r3, "/sub/"+tok3, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled user: %d", rec.Code)
	}
}

// TestSubDBErrors covers the four data-fetch failure branches: usage, entry list, linkage downgrade, RTT aggregation.
func TestSubDBErrors(t *testing.T) {
	// Usage fetch failure -> 500 (Scan goes through the Row callback chain rather than the Query chain, so the error is injected by Model).
	r, db, tok := subFixture(t)
	db.Callback().Row().Before("gorm:row").Register("t:subusage", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.TrafficLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("usage query fail: %d %s", rec.Code, rec.Body)
	}

	// Entry list fetch failure -> 500.
	r2, db2, tok2 := subFixture(t)
	regQueryFail("t:subnodes", db2, (*[]storage.Node)(nil))
	if rec := getSub(t, r2, "/sub/"+tok2, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes query fail: %d %s", rec.Code, rec.Body)
	}

	// Linkage downgrade fetch failure -> 500 (the injected error is not a record-miss error).
	r3, db3, tok3 := subFixture(t)
	regQueryFail("t:subaction", db3, (*storage.QuotaAction)(nil))
	if rec := getSub(t, r3, "/sub/"+tok3, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("action query fail: %d %s", rec.Code, rec.Body)
	}

	// RTT aggregation fetch failure -> degrades to no latency, the subscription still returns 200 (the aggregation query is also a Scan, so the error is injected by Model).
	r4, db4, tok4 := subFixture(t)
	db4.Callback().Row().Before("gorm:row").Register("t:subrtt", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.ProbeReport); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := getSub(t, r4, "/sub/"+tok4, "v2rayNG/1.8"); rec.Code != http.StatusOK {
		t.Fatalf("rtt query fail should degrade: %d %s", rec.Code, rec.Body)
	}
}

// TestSubPackErrors covers packing failure for all three targets -> 500 when an entry node's config is unparseable.
func TestSubPackErrors(t *testing.T) {
	r, db, tok := subFixture(t)
	node := storage.Node{
		Name: "pack-bad", Address: "a.example.com", Port: 443, Protocol: "vless",
		Config: "{bad", Enabled: true, Role: "entry", PoolState: "active", Status: "online",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", "clash", "singbox"} {
		path := "/sub/" + tok
		if target != "" {
			path += "?target=" + target
		}
		if rec := getSub(t, r, path, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
			t.Fatalf("pack %s: %d %s", target, rec.Code, rec.Body)
		}
	}
}

// TestSubRttRemark covers entry latency aggregation hits: link remarks carry (Nms)
// when recent probe verdicts exist, and omit the parenthesis without probe data.
func TestSubRttRemark(t *testing.T) {
	nodeCfg := `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`
	decode := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		// Link fragments are URI-encoded (non-ASCII such as regions/parentheses plus reserved chars), so unescape before comparing.
		out, err := url.PathUnescape(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// No probe data: no latency parenthesis.
	r, db, tok := subFixture(t)
	n := storage.Node{Name: "rtt-a", Address: "a.example.com", Port: 443, Protocol: "vless",
		Config: nodeCfg, Enabled: true, Role: "entry", PoolState: "active", Status: "online"}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	rec := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(decode(rec), "ms)") {
		t.Fatalf("no probe data should have no rtt remark: %s", decode(rec))
	}

	// Recent probe verdicts: remarks carry (42ms).
	r2, db2, tok2 := subFixture(t)
	if err := db2.Create(&storage.Node{Name: "rtt-b", Address: "b.example.com", Port: 443,
		Protocol: "vless", Config: nodeCfg, Enabled: true, Role: "entry",
		PoolState: "active", Status: "online"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db2.Create(&storage.ProbeReport{NodeID: 1, TargetKind: "tunnel",
		Direction: "out", RttMs: 42, Reachable: true, Verdict: "healthy",
		Region: "未知", ISP: "未知", ProbedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	rec2 := getSub(t, r2, "/sub/"+tok2, "v2rayNG/1.8")
	if rec2.Code != http.StatusOK {
		t.Fatalf("sub with rtt: %d %s", rec2.Code, rec2.Body)
	}
	if !strings.Contains(decode(rec2), "42ms)") {
		t.Fatalf("rtt remark missing: %s", decode(rec2))
	}
}
