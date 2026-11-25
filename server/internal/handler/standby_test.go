package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// Panel master/standby sync (P2-1) primary-panel endpoint tests: the token
// gate (unconfigured/mismatched always 403), and the artifact issued = a
// master-key-encrypted sqlite db (openable by standby after decryption).

// TestStandbySnapshot covers the snapshot endpoint's authentication and artifact shape.
func TestStandbySnapshot(t *testing.T) {
	dir := t.TempDir()
	key := "standby-master"

	get := func(tokenConfig, tokenSent string) *httptest.ResponseRecorder {
		eng, _ := newTestRouterCfg(t, func(c *config.Config) {
			c.StandbyToken = tokenConfig
			c.BackupDir = dir
			c.SecretKey = key
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/standby/snapshot", nil)
		if tokenSent != "" {
			req.Header.Set("X-Standby-Token", tokenSent)
		}
		eng.ServeHTTP(rec, req)
		return rec
	}

	// No token configured: the sync surface is off; 403 with or without header.
	if rec := get("", "tok"); rec.Code != http.StatusForbidden {
		t.Fatalf("no token configured: %d", rec.Code)
	}
	if rec := get("", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no token no header: %d", rec.Code)
	}
	// Token mismatch: 403.
	if rec := get("tok", "wrong"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	// Token match: 200, the decrypted artifact is a sqlite db (with business table schema).
	rec := get("tok", "tok")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: %d %s", rec.Code, rec.Body)
	}
	store := secret.NewStore(key)
	raw, err := store.DecryptBytes(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if len(raw) < 16 || string(raw[:15]) != "SQLite format 3" {
		t.Fatal("snapshot is not a sqlite db")
	}
	// Write to disk to confirm it opens (what the standby side will do).
	p := filepath.Join(dir, "snap.db")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := storage.Open(storage.DriverSQLite, p)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	if sqlDB, err := fresh.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
