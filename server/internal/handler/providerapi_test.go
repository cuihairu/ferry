package handler

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// newTestRouterWithMasterKey 建带机密主密钥的测试路由（OS-1 凭证录入用），
// 返回同主密钥的独立 store 供验密文。
func newTestRouterWithMasterKey(t *testing.T) (*gin.Engine, *gorm.DB) {
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
	cfg.SecretKey = "test-master-key"
	r, _ := NewRouter(db, cfg)
	return r, db
}

// TestProviderAPI 覆盖提供商凭证（OS-1）：录入加密落库（库中无明文）、
// 列表不回显（has_access_key）、更换机密、空机密保留、有模板的提供商拒绝删除、
// 未配主密钥的实例拒绝录入。
func TestProviderAPI(t *testing.T) {
	r, db := newTestRouterWithMasterKey(t)
	store := secret.NewStore("test-master-key")

	// 录入
	rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
		"name": "vultr-main", "type": "vultr", "access_key": "sk-live-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create provider status = %d body=%s", rec.Code, rec.Body)
	}
	var pv struct {
		ID           uint   `json:"id"`
		HasAccessKey bool   `json:"has_access_key"`
		AccessKey    string `json:"access_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil {
		t.Fatal(err)
	}
	if !pv.HasAccessKey || pv.AccessKey != "" {
		t.Fatalf("provider view leaks key: %+v", pv)
	}

	// 库里是密文且可解回
	var row storage.Provider
	if err := db.First(&row, pv.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.AccessKey == "sk-live-secret" {
		t.Fatal("access key must be stored encrypted")
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-live-secret" {
		t.Fatalf("decrypt stored key = %q err=%v", plain, err)
	}

	// 列表掩码：只有 has_access_key，无明文
	rec = doJSON(t, r, "GET", "/api/providers", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if !json.Valid([]byte(body)) || !containsStr(body, `"has_access_key":true`) || containsStr(body, "sk-live-secret") {
		t.Fatalf("list leaks key: %s", body)
	}

	// 更换机密
	id := strconv.FormatUint(uint64(pv.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"access_key": "sk-new"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update provider status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatalf("rotated key = %q err=%v", plain, err)
	}

	// 空机密保留：只改名
	rec = doJSON(t, r, "PUT", "/api/providers/"+id, map[string]any{"name": "vultr-renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d", rec.Code)
	}
	db.First(&row, pv.ID)
	if row.Name != "vultr-renamed" {
		t.Fatalf("name = %s", row.Name)
	}
	if plain, err := store.Decrypt(row.AccessKey); err != nil || plain != "sk-new" {
		t.Fatal("rename must keep access key")
	}

	// 有模板引用时拒绝删除
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": pv.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "direction": "out", "line_type": "163", "role": "entry",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/providers/"+id, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete provider with templates status = %d", rec.Code)
	}

	// 未配主密钥的实例拒绝录入（禁止明文落库）
	r2, _ := newTestRouterWithDB(t)
	rec = doJSON(t, r2, "POST", "/api/providers", map[string]any{
		"name": "x", "type": "vultr", "access_key": "sk",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create without master key status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestTemplateAPI 覆盖机型模板（OS-1）：CRUD 与字段校验。
func TestTemplateAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: "v1:a:b", Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}

	// 非法方向拒绝
	rec := doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "bad", "provider_id": prov.ID, "direction": "sideways",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid direction status = %d", rec.Code)
	}
	// 缺 provider 拒绝
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{"name": "bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider status = %d", rec.Code)
	}

	// 建模板（缺省 direction=out role=entry）
	rec = doJSON(t, r, "POST", "/api/provision-templates", map[string]any{
		"name": "hk-3t", "provider_id": prov.ID, "plan": "vc2-1c-1gb", "region": "hkg",
		"bw_mbps": 500, "billing_type": "包月", "monthly_cost_cents": 500,
		"traffic_price_cents": 0, "line_type": "163", "role": "entry", "transport": "ws-tls",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create template status = %d body=%s", rec.Code, rec.Body)
	}
	var tpl struct {
		ID               uint   `json:"id"`
		Name             string `json:"name"`
		Direction        string `json:"direction"`
		Role             string `json:"role"`
		BillingType      string `json:"billing_type"`
		MonthlyCostCents int64  `json:"monthly_cost_cents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tpl); err != nil {
		t.Fatal(err)
	}
	if tpl.Direction != "out" || tpl.Role != "entry" || tpl.BillingType != "包月" || tpl.MonthlyCostCents != 500 {
		t.Fatalf("template = %+v", tpl)
	}

	// 改与删
	tid := strconv.FormatUint(uint64(tpl.ID), 10)
	rec = doJSON(t, r, "PUT", "/api/provision-templates/"+tid, map[string]any{
		"name": "hk-3t-v2", "provider_id": prov.ID, "plan": "vc2-2c-2gb", "region": "hkg",
		"billing_type": "按流量", "traffic_price_cents": 30, "direction": "in", "line_type": "cn2_gia", "role": "both",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update template status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/provision-templates", nil)
	if rec.Code != http.StatusOK || !containsStr(rec.Body.String(), "hk-3t-v2") {
		t.Fatalf("list templates = %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "DELETE", "/api/provision-templates/"+tid, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete template status = %d", rec.Code)
	}
	var n int64
	db.Model(&storage.ProvisionTemplate{}).Count(&n)
	if n != 0 {
		t.Fatalf("templates after delete = %d", n)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
