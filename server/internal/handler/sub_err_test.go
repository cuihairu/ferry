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

// subFixture 起路由并建一个有效订阅用户（配额 1000、2030 到期），返回
// 路由、库与订阅令牌。
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

// TestSubUserErrors 覆盖订阅链用户查询失败与停用用户按 404 处理。
func TestSubUserErrors(t *testing.T) {
	// 未知令牌 → 404（防枚举，不计失败次数之外的其它路径）。
	r, _, _ := subFixture(t)
	if rec := getSub(t, r, "/sub/no-such-token", "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}

	// 用户查询库故障（非查无）→ 500。
	r2, db2, tok2 := subFixture(t)
	regQueryFail("t:subuser", db2, (*storage.User)(nil))
	if rec := getSub(t, r2, "/sub/"+tok2, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("user query fail: %d %s", rec.Code, rec.Body)
	}

	// 停用用户 → 404（令牌视同吊销）。
	r3, db3, tok3 := subFixture(t)
	if err := db3.Model(&storage.User{}).Where("sub_token = ?", tok3).
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if rec := getSub(t, r3, "/sub/"+tok3, "v2rayNG/1.8"); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled user: %d", rec.Code)
	}
}

// TestSubDBErrors 覆盖用量、入口列表、联动降档、延迟聚合四处取数失败分支。
func TestSubDBErrors(t *testing.T) {
	// 用量取数失败 → 500（Scan 走 Row 回调链而非 Query 链，按 Model 注错）。
	r, db, tok := subFixture(t)
	db.Callback().Row().Before("gorm:row").Register("t:subusage", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.TrafficLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := getSub(t, r, "/sub/"+tok, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("usage query fail: %d %s", rec.Code, rec.Body)
	}

	// 入口列表取数失败 → 500。
	r2, db2, tok2 := subFixture(t)
	regQueryFail("t:subnodes", db2, (*[]storage.Node)(nil))
	if rec := getSub(t, r2, "/sub/"+tok2, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes query fail: %d %s", rec.Code, rec.Body)
	}

	// 联动降档取数失败 → 500（注错返回非查无错误）。
	r3, db3, tok3 := subFixture(t)
	regQueryFail("t:subaction", db3, (*storage.QuotaAction)(nil))
	if rec := getSub(t, r3, "/sub/"+tok3, "v2rayNG/1.8"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("action query fail: %d %s", rec.Code, rec.Body)
	}

	// 延迟聚合取数失败 → 退化成无延迟，订阅仍 200（聚合查询同为 Scan，按 Model 注错）。
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

// TestSubPackErrors 覆盖入口节点配置不可解析时三种 target 打包失败 → 500。
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

// TestSubRttRemark 覆盖入口延迟聚合命中：有近期探测结论时链接备注带
// (Nms)，无探测数据时省略括注。
func TestSubRttRemark(t *testing.T) {
	nodeCfg := `{"uuid":"u-1","tls":true,"sni":"s.com","net":"ws","path":"/wss","host":"cdn.example.com"}`
	decode := func(rec *httptest.ResponseRecorder) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		// 链接片段按 URI 编码（区域/括号等非 ASCII 与保留字符），先还原再比对。
		out, err := url.PathUnescape(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// 无探测数据：无括注延迟。
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

	// 有近期探测结论：备注带 (42ms)。
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
