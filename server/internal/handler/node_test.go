package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/server/internal/database"
	"github.com/gin-gonic/gin"
)

// newTestRouter 建独立临时库的 gin 测试引擎。
func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	gin.SetMode(gin.TestMode)
	return NewRouter(db)
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
