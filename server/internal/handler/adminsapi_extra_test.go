package handler

import (
	"net/http"
	"testing"
)

// TestCreateAdminDuplicateConflict 重名管理员：409 冲突，不冒称创建成功。
func TestCreateAdminDuplicateConflict(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	body := map[string]any{"username": "dup-op", "password": "password123"}
	if rec := doJSON(t, r, "POST", "/api/admins", body); rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "POST", "/api/admins", body); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate create: %d %s; want 409", rec.Code, rec.Body)
	}
}
