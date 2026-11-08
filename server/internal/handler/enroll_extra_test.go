package handler

import (
	"time"
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/model"
)

// TestEnrollTxEmptyToken 覆盖 enrollTx 空 token 直接返回无效。
func TestEnrollTxEmptyToken(t *testing.T) {
	_, db := newTestRouterWithDB(t)
	h := &Handler{db: db}
	if _, err := h.enrollTx("", &model.NodeInput{Name: "x"}, time.Now()); err != errEnrollTokenInvalid {
		t.Fatalf("empty token err = %v", err)
	}
}

// TestEnrollTokenListDBError 覆盖 listEnrollmentTokens 的 DB 错误分支。
func TestEnrollTokenListDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
	if rec := doJSON(t, r, "GET", "/api/enrollment-tokens", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list closed db status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestEnrollRevokeUsed 覆盖 revoke 已用凭据 409 与非法 id 400。
func TestEnrollRevokeUsed(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d", rec.Code)
	}
	rec := doJSON(t, r, "POST", "/api/enrollment-tokens", map[string]any{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue status = %d body=%s", rec.Code, rec.Body)
	}
	var id uint
	db.Raw(`SELECT id FROM enrollment_tokens LIMIT 1`).Scan(&id)
	if err := db.Exec(`UPDATE enrollment_tokens SET used_at = CURRENT_TIMESTAMP, used_by='tester' WHERE id = ?`, id).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "DELETE", "/api/enrollment-tokens/"+itoa64(int(id)), nil); rec.Code != http.StatusConflict {
		t.Fatalf("used revoke status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestEnrollNodeBadToken 覆盖 enrollNode 无效凭据 401。
func TestEnrollNodeBadToken(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doEnroll(t, r, "bad-token", enrollValidNodeBody())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d body=%s", rec.Code, rec.Body)
	}
}

// TestEnrollNodeBadPayload 覆盖 enrollNode 坏载荷 400（不消费凭据）。
func TestEnrollNodeBadPayload(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doEnroll(t, r, "tk", map[string]any{"name": ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad payload status = %d body=%s", rec.Code, rec.Body)
	}
}
