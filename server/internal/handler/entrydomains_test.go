package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestEntryDomainsCRUD 覆盖入口域名数据面（TOUCH-3）：建（协议前缀剥离、
// role 校验）、改（部分更新含停用）、列、删、404。
func TestEntryDomainsCRUD(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 非法 role 400、空 domain 400
	if rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "a.com", "role": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad role: %d", rec.Code)
	}
	if rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": " ", "role": "primary"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty domain: %d", rec.Code)
	}

	// 建：https 前缀与尾部斜杠剥离
	rec := doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "https://panel.example.com/", "role": "primary", "region": "cn"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/entry-domains", map[string]any{"domain": "backup.example.org", "role": "backup"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create backup: %d %s", rec.Code, rec.Body)
	}

	// 列：两条按 id 升序
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

	// 改：停用 + 换角色
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"enabled": false, "role": "backup"}); rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/entry-domains", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &rows)
	if rows[0]["enabled"] != false || rows[0]["role"] != "backup" {
		t.Fatalf("update leaked: %+v", rows[0])
	}
	// 非法 role 改动 400
	if rec := doJSON(t, r, "PUT", "/api/entry-domains/1", map[string]any{"role": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad update role: %d", rec.Code)
	}

	// 删：删除后列表一条，再删/再改 404
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
