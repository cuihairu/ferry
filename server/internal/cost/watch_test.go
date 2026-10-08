package cost

import (
	"bytes"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func seedWatch(t *testing.T, db *gorm.DB, provider, region, spec string, target int64) storage.PriceWatch {
	t.Helper()
	w := storage.PriceWatch{Provider: provider, Region: region, Spec: spec, TargetPrice: target, Enabled: true}
	if err := db.Create(&w).Error; err != nil {
		t.Fatal(err)
	}
	return w
}

func priceEvents(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindPriceAlert).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCheckPriceWatches 覆盖价格关注扫描：基线不告警、同价不告警、
// 降价+到位同轮各一条、维持不重复（同日去重 + 到位只认跨越沿）、
// 未导入表整轮 no-op、停用关注不扫。
func TestCheckPriceWatches(t *testing.T) {
	db := newTestDB(t)
	logger := log.New(&bytes.Buffer{}, "", 0)
	now := time.Now()

	// 未导入价格表：整轮 no-op。
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("no table scan: %v", err)
	}

	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":1000}]`)); err != nil {
		t.Fatal(err)
	}
	w := seedWatch(t, db, "vultr", "tokyo", "1c", 900)

	// 第 1 轮：基线快照，无上一快照不告警。
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	var snaps []storage.PriceSnapshot
	if err := db.Where("watch_id = ?", w.ID).Find(&snaps).Error; err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].MonthlyCents != 1000 {
		t.Fatalf("snapshots after scan1 = %+v", snaps)
	}
	if n := priceEvents(t, db); n != 0 {
		t.Fatalf("scan1 events = %d", n)
	}

	// 第 2 轮：同价——无降价、未到目标。
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if n := priceEvents(t, db); n != 0 {
		t.Fatalf("scan2 events = %d", n)
	}

	// 第 3 轮：降到 800（-20%，且越过目标价 900）——降价与到位各一条。
	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":800}]`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 3: %v", err)
	}
	if n := priceEvents(t, db); n != 2 {
		t.Fatalf("scan3 events = %d, want 2", n)
	}

	// 第 4 轮：价格维持 800——降价不再重复，到位不回发（非跨越沿）。
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 4: %v", err)
	}
	if n := priceEvents(t, db); n != 2 {
		t.Fatalf("scan4 events = %d, want still 2", n)
	}

	// 停用关注后不再扫快照。
	db.Model(&storage.PriceWatch{}).Where("id = ?", w.ID).Update("enabled", false)
	if err := SaveRefTable(db, []byte(`[{"provider":"vultr","region":"tokyo","spec":"1c","monthly_cents":500}]`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 5: %v", err)
	}
	var cnt int64
	db.Model(&storage.PriceSnapshot{}).Where("watch_id = ?", w.ID).Count(&cnt)
	if cnt != 4 {
		t.Fatalf("snapshots = %d, want 4 (disabled watch not scanned)", cnt)
	}

	// 表里没这个键：静默跳过不落快照、不告警。
	seedWatch(t, db, "nosuch", "x", "y", 0)
	if err := CheckPriceWatches(db, now, logger); err != nil {
		t.Fatalf("scan 6: %v", err)
	}
	var ev int64
	db.Model(&storage.Event{}).Where("kind = ?", herald.KindPriceAlert).Count(&ev)
	if ev != 2 {
		t.Fatalf("events after miss scan = %d", ev)
	}
}
