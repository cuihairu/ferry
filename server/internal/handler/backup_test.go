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

// adminJWT signs a test admin token (the default-secret convention of
// adminAuthMiddleware), bound to the admin with ID=1 (the first seeded admin
// in most cases).
func adminJWT(t *testing.T) string {
	t.Helper()
	return adminJWTFor(t, 1)
}

// adminJWTFor signs a test token for a given admin ID (adminUserID takes the
// Jti, matching the shape of a real AdminLogin token); multi-admin cases (the P2
// 2FA key-partitioning regression) mint one per ID.
func adminJWTFor(t *testing.T, id uint) string {
	t.Helper()
	now := time.Now()
	claims := adminClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "1",
			ID:        strconv.FormatUint(uint64(id), 10),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now), // password-change invalidation uses the issuance time (2026-11-12)
		},
		IsAdmin: true,
	}
	ss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("ferry-admin-secret"))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return ss
}

// TestBackupDB covers backup download end to end (P1-6; from the P1 scheduled
// backup batch the archived copy stores a manual row): the download body is the
// archived file itself (a valid SQLite file whose snapshot shows the created
// user), and the backups table stores a kind=manual row with uploaded always 0
// (a manual archive is not uploaded).
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

	// Open the written snapshot with an independent connection to verify its content is intact.
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
	// root is the admin row required by the /admin group's DB round-trip check
	// (seedAdminForJWT); the business assertions only care about bk.
	var bk *storage.User
	for i := range users {
		if users[i].Username == "bk" {
			bk = &users[i]
		}
	}
	if bk == nil || bk.QuotaBytes != 42 {
		t.Fatalf("snapshot users mismatch: %+v", users)
	}

	// No token is 401.
	if rec := doBare(t, r, "GET", "/admin/backup/db"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("backup without token: %d", rec.Code)
	}

	// Archival and trace: an archive file sits in the backup directory, and the
	// manual row is stored with uploaded always 0 (no outbound upload).
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

// TestBackupDBEncrypted covers a manual backup with a master key configured:
// the archive is the .enc encrypted format (the ciphertext head is the version
// byte), the download body matches the archived file, and a manual row is still
// stored.
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
	if len(raw) < 2 || raw[0] != 1 { // the version byte head of secret.Version=1
		t.Fatalf("cipher head = %v, want version byte 1", raw[:1])
	}
	if string(raw) != w.Body.String() {
		t.Fatal("download body differs from archived file")
	}
}
