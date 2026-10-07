package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// adminJWT 签发测试用管理员令牌（adminAuthMiddleware 默认秘钥口径）。
func adminJWT(t *testing.T) string {
	t.Helper()
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		IsAdmin: true,
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

// TestBackupDB 覆盖备份下载端到端（P1-6）：下载体是合法 SQLite 文件，
// 且快照里能查到已建用户。
func TestBackupDB(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "bk", "quota_bytes": 42})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed user: %d %s", rec.Code, rec.Body)
	}

	req := httptest.NewRequest("GET", "/admin/backup/db", nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", w.Code, w.Body)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", cd)
	}

	// 快照落盘后用独立连接打开，验证内容完整
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(snapshot, w.Body.Bytes(), 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	sdb, err := gorm.Open(sqlite.Open(snapshot), &gorm.Config{})
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	var users []storage.User
	if err := sdb.Find(&users).Error; err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if len(users) != 1 || users[0].Username != "bk" || users[0].QuotaBytes != 42 {
		t.Fatalf("snapshot users mismatch: %+v", users)
	}

	// 无令牌 401
	if rec := doJSON(t, r, "GET", "/admin/backup/db", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("backup without token: %d", rec.Code)
	}
}
