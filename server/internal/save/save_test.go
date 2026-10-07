package save

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedTraffic 直种流量行（按上报时刻分行）。
func seedTraffic(t *testing.T, db *gorm.DB, rows ...storage.NodeTrafficLog) {
	t.Helper()
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
}

func statRows(t *testing.T, db *gorm.DB) []storage.SaveStat {
	t.Helper()
	var rows []storage.SaveStat
	if err := db.Order("day ASC, node_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query save stats: %v", err)
	}
	return rows
}

// TestSweepAggregatesByDay 覆盖主通路：流量行按 (节点, UTC 日) 聚合落
// save_stats，二轮同水位不重复累计，跨日分行。
func TestSweepAggregatesByDay(t *testing.T) {
	db := newTestDB(t)
	day1 := time.Date(2026, 10, 7, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 100, BlockedBytes: 10, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 50, BlockedBytes: 5, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 2, Proc: "xray", DirectBytes: 7, RecordedAt: day1},
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 200, BlockedBytes: 20, RecordedAt: day2},
	)

	if n, err := Sweep(db); err != nil || n != 4 {
		t.Fatalf("sweep = %d, %v; want 4 rows", n, err)
	}
	rows := statRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("save stats = %d rows, want 3 (2 节点×同日 + 1 跨日): %+v", len(rows), rows)
	}
	// 节点1 day1：两次上报求和；节点1 day2 独立一行
	if rows[0].NodeID != 1 || rows[0].Day != "2026-10-07" || rows[0].DirectBytes != 150 || rows[0].BlockedBytes != 15 {
		t.Fatalf("node1 day1 mismatch: %+v", rows[0])
	}
	if rows[1].NodeID != 2 || rows[1].Day != "2026-10-07" || rows[1].DirectBytes != 7 || rows[1].BlockedBytes != 0 {
		t.Fatalf("node2 day1 mismatch: %+v", rows[1])
	}
	if rows[2].NodeID != 1 || rows[2].Day != "2026-10-08" || rows[2].DirectBytes != 200 || rows[2].BlockedBytes != 20 {
		t.Fatalf("node1 day2 mismatch: %+v", rows[2])
	}

	// 二轮：水位已推进，不重复累计
	if n, err := Sweep(db); err != nil || n != 0 {
		t.Fatalf("second sweep = %d, %v; want 0", n, err)
	}
	rows = statRows(t, db)
	if rows[0].DirectBytes != 150 {
		t.Fatalf("second sweep must not double count: %+v", rows[0])
	}

	// 增量到账：水位之后的新行只累计新增量
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 25, RecordedAt: day2},
	)
	if n, err := Sweep(db); err != nil || n != 1 {
		t.Fatalf("third sweep = %d, %v; want 1", n, err)
	}
	rows = statRows(t, db)
	if rows[2].DirectBytes != 225 {
		t.Fatalf("incremental aggregate = %d, want 225", rows[2].DirectBytes)
	}
}

// TestSweepRebuildsWithoutWatermark 覆盖水位丢失：清空 save_stats 全量
// 重建，不与历史行叠加重复。
func TestSweepRebuildsWithoutWatermark(t *testing.T) {
	db := newTestDB(t)
	day := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	seedTraffic(t, db,
		storage.NodeTrafficLog{NodeID: 1, Proc: "xray", DirectBytes: 100, RecordedAt: day},
	)
	if _, err := Sweep(db); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// 模拟水位丢失但 save_stats 有历史行
	if err := db.Where("key=?", settingWatermark).Delete(&storage.Setting{}).Error; err != nil {
		t.Fatalf("drop watermark: %v", err)
	}
	if _, err := Sweep(db); err != nil {
		t.Fatalf("rebuild sweep: %v", err)
	}
	rows := statRows(t, db)
	if len(rows) != 1 || rows[0].DirectBytes != 100 {
		t.Fatalf("rebuild must replace, not accumulate: %+v", rows)
	}
}
