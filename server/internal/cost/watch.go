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

// 价格关注与促销提醒（E-32，套餐与成本设计 §4.2）：关注条件盯一个牌价键
// （商家/区域/配置档，与参考价表同词表），扫描周期从已导入价格表抓快照；
// 命中降价或现价到位（target_price 跨越沿）经 Herald price_alert 提示——
// 未配 Herald 落本地 outbox 站内可见。只提示，不改任何手录价。

// CheckPriceWatches 单轮扫描：逐条启用关注抓牌价落快照并比对命中。
// 未导入价格表时整轮 no-op（关注还查不到牌价，不算错）。
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

// checkOneWatch 单条关注：抓牌价→落快照→与上一快照比对降价、与目标价
// 比对到位（跨越沿才发，到位后维持不重复告警）。
func checkOneWatch(db *gorm.DB, src costref.Source, w storage.PriceWatch, now time.Time) error {
	quote, err := src.Lookup(context.Background(), costref.Query{
		Provider: w.Provider, Region: w.Region, Spec: w.Spec,
	})
	if errors.Is(err, costref.ErrNotFound) {
		return nil // 表里还没这个键，等价格表更新
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

// emitPriceAlert 发管理告警：同 kind+dedup_key 已有事件则跳过（同日至多
// 一条口径，与 login_alert 一致）；未配 Herald 经 Emit 落本地 outbox 站内可见。
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

// WatchLoop 周期扫描价格关注（main 装配；interval<=0 回落 1h）。
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
