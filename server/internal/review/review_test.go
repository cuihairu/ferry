package review

import (
	"context"
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
		{Username: "expired", SubToken: "t-expired", Enabled: true, ExpiresAt: &past},   // should stop: already expired
		{Username: "valid-exp", SubToken: "t-valid", Enabled: true, ExpiresAt: &future}, // keep: not yet expired
		{Username: "no-expiry", SubToken: "t-noexp", Enabled: true},                     // keep: no expiry
		{Username: "already-off", SubToken: "t-off", Enabled: false, ExpiresAt: &past},  // untouched: already disabled
		{Username: "over-quota", SubToken: "t-quota", Enabled: true, QuotaBytes: 100},   // should stop: over quota (usage seeded below)
		{Username: "under-quota", SubToken: "t-under", Enabled: true, QuotaBytes: 100},  // keep: under quota
		{Username: "unlimited", SubToken: "t-unl", Enabled: true, QuotaBytes: 0},        // keep: unlimited quota
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// Enabled carries default:true, so Create cannot write false directly; explicitly patch the disabled state.
	if err := db.Model(&storage.User{}).Where("username=?", "already-off").
		Update("enabled", false).Error; err != nil {
		t.Fatalf("seed disabled user: %v", err)
	}
	u4, u5, u6 := users[4].ID, users[5].ID, users[6].ID
	traffic := []storage.TrafficLog{
		{UserID: &u4, RxBytes: 60, TxBytes: 50, RecordedAt: time.Now()}, // over-quota: 110 >= 100
		{UserID: &u5, RxBytes: 40, TxBytes: 20, RecordedAt: time.Now()}, // under-quota: 60 < 100
		{UserID: &u6, RxBytes: 999, TxBytes: 999, RecordedAt: time.Now()},
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

	// day user: 200 last month exceeds the 100 quota, but out of window it does not count; today's 60 is under → not disabled.
	dayUser := storage.User{Username: "day-u", SubToken: "c1", Enabled: true, QuotaBytes: 100, ResetCycle: "day"}
	// none user: the same traffic distribution, total 260 >= 100 → disabled.
	noneUser := storage.User{Username: "none-u", SubToken: "c2", Enabled: true, QuotaBytes: 100, ResetCycle: "none"}
	users := []storage.User{dayUser, noneUser}
	if err := db.Create(&users).Error; err != nil {
		t.Fatalf("seed users: %v", err)
	}
	dayUser, noneUser = users[0], users[1]

	now := time.Now()
	du, nu := dayUser.ID, noneUser.ID
	traffic := []storage.TrafficLog{
		{UserID: &du, RxBytes: 100, TxBytes: 100, RecordedAt: now.Add(-30 * 24 * time.Hour)}, // last month
		{UserID: &du, RxBytes: 60, TxBytes: 0, RecordedAt: now.Add(-time.Hour)},              // today
		{UserID: &nu, RxBytes: 100, TxBytes: 100, RecordedAt: now.Add(-30 * 24 * time.Hour)},
		{UserID: &nu, RxBytes: 60, TxBytes: 0, RecordedAt: now.Add(-time.Hour)},
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

// TestRunZeroIntervalReturns covers Run's disabled criteria: interval<=0 returns directly without starting the loop.
func TestRunZeroIntervalReturns(t *testing.T) {
	Run(context.Background(), nil, 0) // should not panic and not start a ticker
}

// TestRunTickDisablesAndStopsOnCancel covers the Run loop: DisableInactive runs on tick
// (the expired user is disabled), and it exits after ctx cancel.
func TestRunTickDisablesAndStopsOnCancel(t *testing.T) {
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
	if err := db.Create(&storage.User{Username: "expired", SubToken: "t-run", Enabled: true, ExpiresAt: &past}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 20*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var u storage.User
		if err := db.Where("username=?", "expired").First(&u).Error; err != nil {
			t.Fatalf("load user: %v", err)
		}
		if !u.Enabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run tick never disabled expired user")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}
