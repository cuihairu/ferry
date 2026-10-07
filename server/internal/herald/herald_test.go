package herald

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newHeraldDB(t *testing.T) *gorm.DB {
	t.Helper()
	// 按测试名+时刻隔离内存库：cache=shared 全包共库会跨测试残留事件行。
	dsn := fmt.Sprintf("file:herald-%s-%d?mode=memory&cache=shared", t.Name(), time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&storage.Event{}, &storage.EventDelivery{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func emitOne(t *testing.T, db *gorm.DB, kind string) *storage.Event {
	t.Helper()
	ev, err := Emit(db, EmitInput{
		Kind: kind, Severity: SeverityCritical,
		Title: "华东区域入口整体不可达", Body: "3/3 节点 sick",
		Target: "admin", DedupKey: "region:华东:region_fault",
		Meta:    map[string]any{"region": "华东", "count": 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestEmit(t *testing.T) {
	db := newHeraldDB(t)
	ev := emitOne(t, db, "region_fault")

	if ev.Status != StatusPending || ev.Kind != "region_fault" || ev.Target != "admin" {
		t.Fatalf("event = %+v", ev)
	}
	if ev.DedupKey != "region:华东:region_fault" {
		t.Fatalf("dedup_key = %q", ev.DedupKey)
	}
	if ev.Meta != `{"count":3,"region":"华东"}` {
		t.Fatalf("meta = %q", ev.Meta)
	}
	// 非法严重度归 info，零时刻归当前
	ev2, err := Emit(db, EmitInput{Kind: "x", Severity: "boom", Title: "t"})
	if err != nil || ev2.Severity != SeverityInfo {
		t.Fatalf("invalid severity should fall back info: %+v err=%v", ev2, err)
	}
	if ev2.OccurredAt.IsZero() {
		t.Fatal("zero occurred_at should default to now")
	}
}

func TestDeliverSuccess(t *testing.T) {
	db := newHeraldDB(t)
	ev := emitOne(t, db, "node_blocked")
	calls := 0
	sent, err := Deliver(db, time.Now(), func(e storage.Event) error {
		calls++
		if e.ID != ev.ID {
			t.Fatalf("unexpected event %d", e.ID)
		}
		return nil
	})
	if err != nil || sent != 1 || calls != 1 {
		t.Fatalf("delivered=%d calls=%d err=%v", sent, calls, err)
	}
	var got storage.Event
	db.First(&got, ev.ID)
	if got.Status != StatusSent || got.Attempts != 1 || got.NextAttemptAt != nil {
		t.Fatalf("event after success = %+v", got)
	}
	// 投递留痕落行
	var dl storage.EventDelivery
	if err := db.Where("event_id = ?", ev.ID).First(&dl).Error; err != nil ||
		dl.Channel != ChannelHerald || dl.Status != "sent" {
		t.Fatalf("delivery = %+v err=%v", dl, err)
	}
	// 二轮不再投
	sent, err = Deliver(db, time.Now().Add(time.Minute), func(storage.Event) error {
		t.Fatal("sent event should not be re-delivered")
		return nil
	})
	if err != nil || sent != 0 {
		t.Fatalf("second sweep delivered=%d err=%v", sent, err)
	}
}

func TestDeliverRetryBackoffAndDeadLetter(t *testing.T) {
	db := newHeraldDB(t)
	ev := emitOne(t, db, "cert_expiring")
	fail := func(storage.Event) error { return errors.New("herald unreachable") }

	now := time.Now()
	prevNext := time.Time{}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if _, err := Deliver(db, now, fail); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		var got storage.Event
		db.First(&got, ev.ID)
		if got.Attempts != attempt {
			t.Fatalf("attempt counter = %d, want %d", got.Attempts, attempt)
		}
		if attempt < MaxAttempts {
			if got.Status != StatusPending {
				t.Fatalf("attempt %d status = %s, want pending", attempt, got.Status)
			}
			if got.NextAttemptAt == nil || !got.NextAttemptAt.After(now) {
				t.Fatalf("attempt %d next_attempt_at = %v, want future", attempt, got.NextAttemptAt)
			}
			// 退避递增且封顶
			want := backoff(attempt)
			if want > maxDelay {
				want = maxDelay
			}
			if got.NextAttemptAt.Sub(now) != want {
				t.Fatalf("attempt %d delay = %v, want %v", attempt, got.NextAttemptAt.Sub(now), want)
			}
			// 未到期不重投
			if n, _ := Deliver(db, got.NextAttemptAt.Add(-time.Second), fail); n != 0 {
				t.Fatalf("attempt %d: early retry happened", attempt)
			}
			now = *got.NextAttemptAt
			prevNext = *got.NextAttemptAt
		} else {
			if got.Status != StatusFailed {
				t.Fatalf("after %d attempts status = %s, want failed", MaxAttempts, got.Status)
			}
			if got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(prevNext) && attempt == MaxAttempts && prevNext.IsZero() {
				t.Fatalf("dead letter next = %v", got.NextAttemptAt)
			}
		}
	}
	// 失败留痕逐次落行
	var n int64
	db.Model(&storage.EventDelivery{}).Where("event_id = ? AND status = ?", ev.ID, "failed").Count(&n)
	if n != MaxAttempts {
		t.Fatalf("failed deliveries = %d, want %d", n, MaxAttempts)
	}
	// 死信不再重投（sender 不该被调到）
	if _, err := Deliver(db, now.Add(24*time.Hour), func(storage.Event) error {
		t.Fatal("dead letter should not be re-delivered")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBackoffCap(t *testing.T) {
	if backoff(1) != time.Minute || backoff(2) != 2*time.Minute || backoff(3) != 4*time.Minute {
		t.Fatal("backoff should double per attempt")
	}
	if backoff(7) != maxDelay || backoff(20) != maxDelay {
		t.Fatal("backoff should cap at 1h")
	}
}

func TestLoopNilSenderKeepsPending(t *testing.T) {
	db := newHeraldDB(t)
	ev := emitOne(t, db, "backup_failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, nil, nil)
		close(done)
	}()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	var got storage.Event
	db.First(&got, ev.ID)
	if got.Status != StatusPending || got.Attempts != 0 {
		t.Fatalf("nil sender must not consume attempts: %+v", got)
	}
}

func TestLoopDelivers(t *testing.T) {
	db := newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	emitOne(t, db, "recovery_failed")
	calls := make(chan int64, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Loop(ctx, db, 20*time.Millisecond, func(e storage.Event) error {
		calls <- e.ID
		return nil
	}, nil)

	deadline := time.After(3 * time.Second)
	seen := map[int64]bool{}
	for len(seen) < 2 {
		select {
		case id := <-calls:
			seen[id] = true
		case <-deadline:
			t.Fatalf("loop did not deliver in time, seen=%v", seen)
		}
	}
	// sender 回调先于 markSent 提交，sent 计数轮询等到提交完成（提交竞态在案）。
	var n int64
	for {
		db.Model(&storage.Event{}).Where("status = ?", StatusSent).Count(&n)
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("sent = %d, want 2 (delivery commit not observed)", n)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
