package herald

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 投递重试口径：指数退避 1m/2m/4m/…封顶 1h，连败 MaxAttempts 轮置 failed
// 死信（dash 标红提示通道故障，事件不删不静默丢，修好通道可人工重投）。
const (
	MaxAttempts = 6
	baseDelay   = time.Minute
	maxDelay    = time.Hour
	batchSize   = 100
)

// Sender 是一次投递尝试：返回 nil=Herald 接收成功。HTTP 实现随 HERALD-2
// （FERRY_HERALD_URL/TOKEN）；未配置通道时 Loop 收到 nil sender，
// 事件留在 outbox 不尝试不烧次数（设计稿 §3：不阻塞主流程）。
type Sender func(storage.Event) error

// backoff 第 n 次失败后的重投等待（n 从 1 起）。
func backoff(n int) time.Duration {
	d := baseDelay
	for i := 1; i < n; i++ {
		d *= 2
		if d >= maxDelay {
			return maxDelay
		}
	}
	return d
}

// Loop 周期投递直到 ctx 取消（启动即跑一轮）。sender 为 nil 时只空转——
// 事件落库等通道配置，不标记不重试。
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, sender Sender, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if sender != nil {
			if n, err := Deliver(db, time.Now(), sender); err != nil {
				logger.Printf("herald deliver: %v", err)
			} else if n > 0 {
				logger.Printf("herald: %d event(s) delivered", n)
			}
		}
		t.Reset(interval)
	}
}

// Deliver 投递一轮到期事件：取到期的 pending（首投或重投时刻已到）逐条
// 尝试，成功置 sent，失败记投递留痕并按退避排下次，超限置 failed 死信。
// 返回本轮成功条数。
func Deliver(db *gorm.DB, now time.Time, sender Sender) (int, error) {
	if sender == nil {
		return 0, nil
	}
	var due []storage.Event
	if err := db.Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)",
		StatusPending, now).Order("id").Limit(batchSize).Find(&due).Error; err != nil {
		return 0, err
	}
	delivered := 0
	for _, ev := range due {
		err := sender(ev)
		attempts := ev.Attempts + 1
		if err == nil {
			if err := markSent(db, ev.ID, attempts, now); err != nil {
				return delivered, err
			}
			delivered++
			continue
		}
		status := StatusPending
		if attempts >= MaxAttempts {
			status = StatusFailed // 死信：重试超限，dash 标红
		}
		if err := markRetry(db, ev.ID, attempts, status, now, err.Error()); err != nil {
			return delivered, err
		}
	}
	return delivered, nil
}

func markSent(db *gorm.DB, id int64, attempts int, now time.Time) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&storage.Event{}).Where("id = ?", id).
			Updates(map[string]any{"status": StatusSent, "attempts": attempts, "next_attempt_at": nil}).Error; err != nil {
			return err
		}
		return tx.Create(&storage.EventDelivery{
			EventID: id, Channel: ChannelHerald, Status: "sent", At: now,
		}).Error
	})
}

func markRetry(db *gorm.DB, id int64, attempts int, status string, now time.Time, detail string) error {
	next := now.Add(backoff(attempts))
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&storage.Event{}).Where("id = ?", id).
			Updates(map[string]any{"status": status, "attempts": attempts, "next_attempt_at": next}).Error; err != nil {
			return err
		}
		return tx.Create(&storage.EventDelivery{
			EventID: id, Channel: ChannelHerald, Status: "failed", Detail: detail, At: now,
		}).Error
	})
}
