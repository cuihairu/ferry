package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newTestRouter 建独立临时库的 gin 测试引擎。
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r, _ := newTestRouterWithDB(t)
	return r
}

// newTestRouterWithDB 额外返回 db 句柄，供直接插入流量等关联数据的用例使用。
func newTestRouterWithDB(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	r, db := newTestRouterCfg(t, nil)
	return r, db
}

// newTestRouterCfg 在默认配置上叠加用例定制（备份目录、主密钥等需要
// 临时路径的项），其余口径与 newTestRouterWithDB 一致。
func newTestRouterCfg(t *testing.T, mutate func(*config.Config)) (*gin.Engine, *gorm.DB) {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	r, _ := NewRouter(db, cfg)
	return r, db
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	// 管理数据面自 2026-10-10 起挂 apiAuth（安全修复）：助手默认带管理员
	// JWT，专测无凭据/坏凭据的用例用 doBare 或直建请求自带 Authorization。
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doBare 发不带凭据的请求：专测「无令牌应 401」等鉴权负例。
func doBare(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doRaw 发原始字符串体（坏 JSON 负例用，doJSON 只能发合法 JSON）：
// 其余口径与 doJSON 一致，默认带管理员 JWT。
func doRaw(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestNodeCRUD(t *testing.T) {
	r := newTestRouter(t)

	// 创建：返回 201，携带生成的接入令牌
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-1", "address": "hk.example.com", "port": 443,
		"protocol": "vless", "config": `{"uuid":"u1","tls":true}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create resp: %v", err)
	}
	if created["token"].(string) == "" {
		t.Fatal("created node must carry agent token")
	}
	if created["status"] != "unknown" {
		t.Fatalf("initial status = %v", created["status"])
	}
	id := created["id"].(float64)

	// 列表与详情
	if rec = doJSON(t, r, "GET", "/api/nodes", nil); rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list len=%d err=%v", len(list), err)
	}

	// 更新：部分字段，未传字段保持
	rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{"port": 8443, "enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body)
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["port"].(float64) != 8443 || updated["address"] != "hk.example.com" || updated["enabled"] != false {
		t.Fatalf("partial update lost fields: %v", updated)
	}
	_ = id

	// 非法协议被拒
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "x", "address": "a", "port": 1, "protocol": "wireguard",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad protocol status = %d", rec.Code)
	}

	// 非法 JSON 配置被拒
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "x", "address": "a", "port": 1, "protocol": "vless", "config": "{bad",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad config status = %d", rec.Code)
	}

	// 删除后再查 404
	if rec = doJSON(t, r, "DELETE", "/api/nodes/1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec = doJSON(t, r, "GET", "/api/nodes/1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d", rec.Code)
	}
}

func TestNodeMetaInput(t *testing.T) {
	r := newTestRouter(t)

	// 创建带元数据：按请求落库（E-24 面板可改口径）
	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-entry", "address": "hk.example.com", "port": 443, "protocol": "vless",
		"role": "entry", "direction": "out", "region": "香港", "isp": "HKT", "line_type": "cn2_gia",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create meta status = %d body=%s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	for k, want := range map[string]any{
		"role": "entry", "direction": "out", "region": "香港", "isp": "HKT", "line_type": "cn2_gia",
	} {
		if created[k] != want {
			t.Fatalf("created[%s] = %v, want %v", k, created[k], want)
		}
	}

	// 未提供元数据的创建：落库默认（订阅分组口径依赖非空）
	rec = doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "plain", "address": "a", "port": 1, "protocol": "vless",
	})
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created["role"] != "landing" || created["region"] != "未知" || created["isp"] != "未知" {
		t.Fatalf("defaults = role:%v region:%v isp:%v", created["role"], created["region"], created["isp"])
	}

	// 更新：只改区域，其余元数据保持
	rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{"region": "圣何塞"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update meta status = %d body=%s", rec.Code, rec.Body)
	}
	var updated map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated["region"] != "圣何塞" || updated["role"] != "entry" || updated["isp"] != "HKT" {
		t.Fatalf("partial meta update lost fields: %v", updated)
	}

	// 非法枚举被拒
	for k, v := range map[string]string{"role": "boss", "direction": "up", "transport": "pigeon"} {
		rec = doJSON(t, r, "PUT", "/api/nodes/1", map[string]any{k: v})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad %s status = %d", k, rec.Code)
		}
	}
}

func TestNodeTokenRotation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	oldToken := "old-node-token"
	node := storage.Node{Name: "rotate", Address: "rotate.example.com", Port: 443,
		Protocol: "vless", Token: oldToken, Enabled: true, Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	rec := doJSON(t, r, "POST", "/api/nodes/1/token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
	}
	var rotated storage.Node
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil || rotated.Token == "" {
		t.Fatalf("decode rotate: %v %q", err, rec.Body)
	}
	if rotated.OldToken != oldToken || rotated.Token == oldToken {
		t.Fatalf("rotation fields: old=%q new=%q", rotated.OldToken, rotated.Token)
	}
	if rotated.TokenRotatedAt == nil || time.Since(*rotated.TokenRotatedAt) > time.Minute {
		t.Fatalf("rotated at = %v", rotated.TokenRotatedAt)
	}

	var row storage.Node
	if err := db.First(&row, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Token != rotated.Token || row.OldToken != oldToken || row.TokenRotatedAt == nil {
		t.Fatalf("rotation not persisted: %+v", row)
	}
}

func TestNodeTokenRevocation(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	oldToken := "grace-token"
	node := storage.Node{Name: "revoke", Address: "revoke.example.com", Port: 443,
		Protocol: "vless", Token: "current-token", OldToken: oldToken,
		TokenRotatedAt: ptrTime(time.Now()), Enabled: true, Status: "online"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("seed node: %v", err)
	}

	if rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{"token": "current-token"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("current token: %d %s", rec.Code, rec.Body)
	}
	rec := doJSON(t, r, "POST", "/api/nodes/1/token/revoke", map[string]any{"token": oldToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}

	var revocations []storage.NodeTokenRevocation
	if err := db.Find(&revocations).Error; err != nil || len(revocations) != 1 || revocations[0].Token != oldToken {
		t.Fatalf("revocations = %+v err=%v", revocations, err)
	}
	var row storage.Node
	if err := db.First(&row, node.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.OldToken != "" || row.TokenRotatedAt != nil || row.Token != "current-token" {
		t.Fatalf("grace not closed: %+v", row)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
