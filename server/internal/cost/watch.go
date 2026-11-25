package cost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Price watches and promotion reminders (E-32, plan & cost design §4.2): each watch targets one
// price key (provider/region/spec, shared vocabulary with the reference table); every scan cycle
// snapshots it from the imported price table; a drop or a target_price crossing fires a Herald
// price_alert (no Herald → local outbox, visible in-panel). Hints only; manual prices are never changed.

// CheckPriceWatches runs one scan pass: for each enabled watch, fetch its quote, store a snapshot
// and compare for hits. With no price table imported the pass is a no-op (quotes unresolvable; not an error).
func CheckPriceWatches(db *gorm.DB, now time.Time, logger *log.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	src, err := LoadRefSource(db)
	if err != nil {
		return err
	}
	if src == nil {
		return nil
	}
	var watches []storage.PriceWatch
	if err := db.Where("enabled = ?", true).Order("id ASC").Find(&watches).Error; err != nil {
		return err
	}
	for _, w := range watches {
		if err := checkOneWatch(db, src, w, now); err != nil {
			logger.Printf("price watch %d: %v", w.ID, err)
		}
	}
	return nil
}

// checkOneWatch handles one watch: fetch quote → store snapshot → compare against the previous snapshot
// for a drop and against the target price for a hit (only crossings fire; staying put does not re-alert).
func checkOneWatch(db *gorm.DB, src costref.Source, w storage.PriceWatch, now time.Time) error {
	quote, err := src.Lookup(context.Background(), costref.Query{
		Provider: w.Provider, Region: w.Region, Spec: w.Spec,
	})
	if errors.Is(err, costref.ErrNotFound) {
		return nil // key not in the table yet; wait for the price table update
	}
	if err != nil {
		return err
	}

	var prev storage.PriceSnapshot
	hasPrev := db.Where("watch_id = ?", w.ID).Order("id DESC").First(&prev).Error == nil

	snap := storage.PriceSnapshot{WatchID: w.ID, MonthlyCents: quote.MonthlyCents,
		Source: src.Name(), URL: quote.URL, CapturedAt: now}
	if err := db.Create(&snap).Error; err != nil {
		return err
	}

	label := w.Provider
	if w.Region != "" {
		label += "/" + w.Region
	}
	label += "/" + w.Spec

	if hasPrev && quote.MonthlyCents < prev.MonthlyCents {
		pct := (prev.MonthlyCents - quote.MonthlyCents) * 100 / prev.MonthlyCents
		emitPriceAlert(db,
			fmt.Sprintf("价格关注降价：%s", label),
			fmt.Sprintf("%s 牌价 %d→%d 分/月（降 %d%%），价格页 %s", label, prev.MonthlyCents, quote.MonthlyCents, pct, quoteURL(quote)),
			fmt.Sprintf("price_drop:%d:%s", w.ID, now.Format("2006-01-02")))
	}
	if w.TargetPrice > 0 && quote.MonthlyCents <= w.TargetPrice &&
		(!hasPrev || prev.MonthlyCents > w.TargetPrice) {
		emitPriceAlert(db,
			fmt.Sprintf("价格关注到位：%s", label),
			fmt.Sprintf("%s 现价 %d 分/月已到目标价 %d 分/月，价格页 %s", label, quote.MonthlyCents, w.TargetPrice, quoteURL(quote)),
			fmt.Sprintf("price_target:%d:%s", w.ID, now.Format("2006-01-02")))
	}
	return nil
}

func quoteURL(q costref.Quote) string {
	if q.URL == "" {
		return "（未附）"
	}
	return q.URL
}

// emitPriceAlert emits an admin alert: skips when an event with the same kind+dedup_key exists
// (at most one per day, same policy as login_alert); without Herald configured it lands in the local outbox, visible in-panel.
func emitPriceAlert(db *gorm.DB, title, body, dedup string) {
	key := "price_alert:" + dedup
	var n int64
	if err := db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindPriceAlert, key).Count(&n).Error; err != nil {
		log.Printf("price alert: %v", err)
		return
	}
	if n > 0 {
		return
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind:     herald.KindPriceAlert,
		Severity: herald.SeverityWarning,
		Title:    title,
		Body:     body,
		Target:   herald.TargetAdmin,
		DedupKey: key,
	}); err != nil {
		log.Printf("price alert: %v", err)
	}
}

// WatchLoop scans price watches on an interval (wired up in main; interval<=0 falls back to 1h).
func WatchLoop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = time.Hour
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := CheckPriceWatches(db, time.Now(), logger); err != nil {
			logger.Printf("price watch scan: %v", err)
		}
		t.Reset(interval)
	}
}
