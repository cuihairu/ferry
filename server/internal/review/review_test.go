package review

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func TestDisableInactive(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	past := time.Now().Add(-24 * time.Hour)
	future := time.Now().Add(24 * time.Hour)
	users := []storage.User{
		{Username: "expired", SubToken: "t-expired", Enabled: true, ExpiresAt: &past},   // 应停：已到期
		{Username: "valid-exp", SubToken: "t-valid", Enabled: true, ExpiresAt: &future}, // 保留：未到期
		{Username: "no-expiry", SubToken: "t-noexp", Enabled: true},                     // 保留：无到期
		{Username: "already-off", SubToken: "t-off", Enabled: false, ExpiresAt: &past},  // 不动：本就停用
		{Username: "over-quota", SubToken: "t-quota", Enabled: true, QuotaBytes: 100},   // 应停：超限（下插用量）
		{Username: "under-quota", SubToken: "t-under", Enabled: true, QuotaBytes: 100},  // 保留：未超
		{Username: "unlimited", SubToken: "t-unl", Enabled: true, QuotaBytes: 0},        // 保留：不限配额
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// Enabled 带 default:true，直接 Create 写不进 false，显式补写停用态。
	if err := db.Model(&storage.User{}).Where("username=?", "already-off").
		Update("enabled", false).Error; err != nil {
		t.Fatalf("seed disabled user: %v", err)
	}
	traffic := []storage.TrafficLog{
		{UserID: users[4].ID, RxBytes: 60, TxBytes: 50, RecordedAt: time.Now()}, // over-quota: 110 >= 100
		{UserID: users[5].ID, RxBytes: 40, TxBytes: 20, RecordedAt: time.Now()}, // under-quota: 60 < 100
		{UserID: users[6].ID, RxBytes: 999, TxBytes: 999, RecordedAt: time.Now()},
	}
	if err := db.Create(&traffic).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	n, err := DisableInactive(db, time.Now())
	if err != nil {
		t.Fatalf("DisableInactive: %v", err)
	}
	if n != 2 {
		t.Fatalf("disabled = %d, want 2", n)
	}

	var out []storage.User
	if err := db.Order("id").Find(&out).Error; err != nil {
		t.Fatalf("load users: %v", err)
	}
	wantOff := map[string]bool{"expired": true, "over-quota": true, "already-off": true}
	for _, u := range out {
		if u.Enabled == wantOff[u.Username] {
			t.Fatalf("user %s enabled=%v, mismatch", u.Username, u.Enabled)
		}
	}
}

func TestDisableInactiveEmpty(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	n, err := DisableInactive(db, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("empty db: n=%d err=%v", n, err)
	}
}
