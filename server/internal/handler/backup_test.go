package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// adminJWT 签发测试用管理员令牌（adminAuthMiddleware 默认秘钥口径），
// 绑定到 ID=1 的管理员（多数用例首个 seed 的管理员）。
func adminJWT(t *testing.T) string {
	t.Helper()
	return adminJWTFor(t, 1)
}

// adminJWTFor 为指定管理员 ID 签发测试用令牌（adminUserID 取 Jti，对齐
// AdminLogin 真实令牌形状）；多管理员用例（P2 2FA 分键回归）按 ID 各发一把。
func adminJWTFor(t *testing.T, id uint) string {
	t.Helper()
	now := time.Now()
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			ID:        strconv.FormatUint(uint64(id), 10),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now), // 改密作废判定取签发时间（2026-11-12）
		},
		IsAdmin: true,
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

// TestBackupDB 覆盖备份下载端到端（P1-6，P1 周期备份批起留档落 manual
// 行）：下载体是落档文件本体（合法 SQLite 文件，快照里能查到已建用户），
// backups 表落 kind=manual 行且 uploaded 恒 0（manual 落档不走外发）。
func TestBackupDB(t *testing.T) {
	dir := t.TempDir()
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.BackupDir = dir })
	seedAdminForJWT(t, db)
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
	// root 是 /admin 组回库复核要求的管理员行（seedAdminForJWT），业务断言只认 bk。
	var bk *storage.User
	for i := range users {
		if users[i].Username == "bk" {
			bk = &users[i]
		}
	}
	if bk == nil || bk.QuotaBytes != 42 {
		t.Fatalf("snapshot users mismatch: %+v", users)
	}

	// 无令牌 401
	if rec := doBare(t, r, "GET", "/admin/backup/db"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("backup without token: %d", rec.Code)
	}

	// 留档与留痕：备份目录有档文件，manual 行落库且 uploaded 恒 0（不走外发）
	var row storage.Backup
	if err := db.Where("kind = ?", "manual").First(&row).Error; err != nil {
		t.Fatalf("manual row: %v", err)
	}
	if row.Uploaded {
		t.Fatal("uploaded must stay false (outbound not implemented)")
	}
	if row.SizeBytes != int64(len(w.Body.Bytes())) {
		t.Fatalf("size = %d, want %d", row.SizeBytes, len(w.Body.Bytes()))
	}
	if _, err := os.Stat(row.Path); err != nil {
		t.Fatalf("archived file missing: %v", err)
	}
}

// TestBackupDBEncrypted 覆盖配置主密钥时的手动备份：档为 .enc 加密格式
// （密文头为版本字节），下载体与落档一致，仍落 manual 行。
func TestBackupDBEncrypted(t *testing.T) {
	dir := t.TempDir()
	r, db := newTestRouterCfg(t, func(cfg *config.Config) {
		cfg.BackupDir = dir
		cfg.SecretKey = "test-master-key"
	})
	seedAdminForJWT(t, db)
	req := httptest.NewRequest("GET", "/admin/backup/db", nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("backup: %d %s", w.Code, w.Body)
	}
	var row storage.Backup
	if err := db.Where("kind = ?", "manual").First(&row).Error; err != nil {
		t.Fatalf("manual row: %v", err)
	}
	if !strings.HasSuffix(row.Path, ".enc") {
		t.Fatalf("path = %s, want .enc suffix", row.Path)
	}
	raw, err := os.ReadFile(row.Path)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(raw) < 2 || raw[0] != 1 { // secret.Version=1 的版本字节头
		t.Fatalf("cipher head = %v, want version byte 1", raw[:1])
	}
	if string(raw) != w.Body.String() {
		t.Fatal("download body differs from archived file")
	}
}
