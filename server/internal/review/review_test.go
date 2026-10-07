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

func TestDisableInactiveRespectsResetCycle(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// day 用户：上月 200 超 100 配额，但窗口外不计；今日 60 未超 → 不停。
	dayUser := storage.User{Username: "day-u", SubToken: "c1", Enabled: true, QuotaBytes: 100, ResetCycle: "day"}
	// none 用户：同样流量分布，全量 260 ≥ 100 → 停。
	noneUser := storage.User{Username: "none-u", SubToken: "c2", Enabled: true, QuotaBytes: 100, ResetCycle: "none"}
	users := []storage.User{dayUser, noneUser}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	dayUser, noneUser = users[0], users[1]

	now := time.Now()
	traffic := []storage.TrafficLog{
		{UserID: dayUser.ID, RxBytes: 100, TxBytes: 100, RecordedAt: now.Add(-30 * 24 * time.Hour)}, // 上月
		{UserID: dayUser.ID, RxBytes: 60, TxBytes: 0, RecordedAt: now.Add(-time.Hour)},              // 今天
		{UserID: noneUser.ID, RxBytes: 100, TxBytes: 100, RecordedAt: now.Add(-30 * 24 * time.Hour)},
		{UserID: noneUser.ID, RxBytes: 60, TxBytes: 0, RecordedAt: now.Add(-time.Hour)},
	}
	if err := db.Create(&traffic).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	n, err := DisableInactive(db, now)
	if err != nil {
		t.Fatalf("DisableInactive: %v", err)
	}
	if n != 1 {
		t.Fatalf("disabled = %d, want 1 (只有 none 用户)", n)
	}
	var out []storage.User
	if err := db.Order("id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{}
	for _, u := range out {
		byName[u.Username] = u.Enabled
	}
	if !byName["day-u"] {
		t.Fatal("day 用户窗口外流量不应计入超限，不该被停用")
	}
	if byName["none-u"] {
		t.Fatal("none 用户全量 260 ≥ 100 应被停用")
	}
}
