package herald

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Delivery retry policy: exponential backoff 1m/2m/4m/..., capped at 1h; MaxAttempts
// consecutive failures mark it failed / dead-lettered (dash flags the channel failure in red; the event
const (
	MaxAttempts = 6
	baseDelay   = time.Minute
	maxDelay    = time.Hour
	batchSize   = 100
)

// Sender is one delivery attempt: nil means Herald accepted it. The HTTP implementation comes with HERALD-2
// (FERRY_HERALD_URL/TOKEN); when the channel is unconfigured, Loop receives a nil sender and
// events stay in the outbox without trying or burning attempts (design §3: do not block the main flow).
type Sender func(storage.Event) error

// backoff is the wait before retry after the n-th failure (n starts at 1).
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

// Loop delivers periodically until ctx is cancelled (one round runs at startup). A nil sender just spins idle -
// events stay persisted awaiting channel configuration, unmarked and unretried.
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

// Deliver runs one round of due events: picks due pending ones (first send or retry time reached),
// attempts each, marks sent on success, logs the delivery trail and schedules the next attempt per backoff on failure,
// and marks over-limit ones failed (dead-lettered).
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
			status = StatusFailed // dead-letter: retry limit exceeded, dash flags in red
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
