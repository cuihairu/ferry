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

// 面板主从同步（P2-1）主面板端点测试：令牌门槛（未配置/不符恒 403）、
// 下发产物 = 主密钥加密的 sqlite 库（standby 解密后可开）。

// TestStandbySnapshot 覆盖快照端点的鉴权与产物形态。
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

	// 未配置 token：同步面关闭，带不带 header 都 403。
	if rec := get("", "tok"); rec.Code != http.StatusForbidden {
		t.Fatalf("no token configured: %d", rec.Code)
	}
	if rec := get("", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no token no header: %d", rec.Code)
	}
	// 令牌不符：403。
	if rec := get("tok", "wrong"); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	// 令牌一致：200，产物解密后是 sqlite 库（含业务表结构）。
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
	// 落盘确认可开（standby 侧将做的事）。
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
