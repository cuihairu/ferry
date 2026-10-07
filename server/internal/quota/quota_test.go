package quota

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func at(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.Local)
}

func TestWindowStart(t *testing.T) {
	cases := []struct {
		name  string
		cycle string
		now   time.Time
		want  time.Time
	}{
		{"当日零点", CycleDay, at(2026, time.October, 7, 15, 34), at(2026, time.October, 7, 0, 0)},
		{"周中归周一", CycleWeek, at(2026, time.October, 7, 15, 34), at(2026, time.October, 5, 0, 0)}, // 10-07 周三
		{"周日归本周一", CycleWeek, at(2026, time.October, 11, 23, 0), at(2026, time.October, 5, 0, 0)},
		{"月初零点", CycleMonth, at(2026, time.October, 7, 15, 34), at(2026, time.October, 1, 0, 0)},
		{"跨月", CycleMonth, at(2026, time.November, 2, 8, 0), at(2026, time.November, 1, 0, 0)},
		{"none 全量", CycleNone, at(2026, time.October, 7, 15, 34), time.Time{}},
		{"未知周期按全量", "weekly", at(2026, time.October, 7, 15, 34), time.Time{}},
	}
	for _, c := range cases {
		if got := WindowStart(c.cycle, c.now); !got.Equal(c.want) {
			t.Fatalf("%s: WindowStart(%s) = %v, want %v", c.name, c.cycle, got, c.want)
		}
	}
}

func TestValidCycle(t *testing.T) {
	for _, c := range []string{"none", "day", "week", "month"} {
		if !ValidCycle(c) {
			t.Fatalf("%s should be valid", c)
		}
	}
	for _, c := range []string{"", "daily", "NONE", "year"} {
		if ValidCycle(c) {
			t.Fatalf("%q should be invalid", c)
		}
	}
}

func TestUsedBytesWindowFilter(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	u := storage.User{Username: "win", SubToken: "t1", Enabled: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	now := at(2026, time.October, 7, 12, 0)
	logs := []storage.TrafficLog{
		{UserID: u.ID, RxBytes: 100, TxBytes: 10, RecordedAt: now.Add(-40 * 24 * time.Hour)}, // 上月
		{UserID: u.ID, RxBytes: 30, TxBytes: 3, RecordedAt: now.Add(-3 * 24 * time.Hour)},    // 本周一前？10-04 周日
		{UserID: u.ID, RxBytes: 5, TxBytes: 1, RecordedAt: now.Add(-time.Hour)},              // 今天
	}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}

	// none：全量 149
	if got, err := UsedBytes(db, u.ID, CycleNone, now); err != nil || got != 149 {
		t.Fatalf("none used = %d err=%v, want 149", got, err)
	}
	// day：只今天 6
	if got, _ := UsedBytes(db, u.ID, CycleDay, now); got != 6 {
		t.Fatalf("day used = %d, want 6", got)
	}
	// week：10-05 周一起 → 只有今天的 6（10-04 的 33 在上周）
	if got, _ := UsedBytes(db, u.ID, CycleWeek, now); got != 6 {
		t.Fatalf("week used = %d, want 6", got)
	}
	// month：10-01 起 → 33 + 6 = 39
	if got, _ := UsedBytes(db, u.ID, CycleMonth, now); got != 39 {
		t.Fatalf("month used = %d, want 39", got)
	}
}
