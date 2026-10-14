package notifyscan

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十二批补口：notifyscan 各错误返回口径、流量预警阈值回落与循环内错误日志。

func dropTable(t *testing.T, db *gorm.DB, dest any) {
	t.Helper()
	if err := db.Migrator().DropTable(dest); err != nil {
		t.Fatalf("drop table: %v", err)
	}
}

// failCreates 让后续所有插入失败（gorm 创建回调注错）。
func failCreates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Create().Before("gorm:create").Register("test:fail_create", func(tx *gorm.DB) {
		tx.AddError(errors.New("db down"))
	})
}

// bufLogger 线程安全日志缓冲（Loop 在独立 goroutine 写）。
type bufLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *bufLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// waitForLog 轮询等待日志出现期望串。
func waitForLog(t *testing.T, want string, l *bufLogger) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(l.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log lacks %q: %s", want, l.String())
}

// seedExpiringUser 造一个 7 天内到期且开了到期偏好的用户。
func seedExpiringUser(t *testing.T, db *gorm.DB) storage.User {
	t.Helper()
	return seedUser(t, db, storage.User{
		Username: "exp-err", SubToken: "e1", Enabled: true, NotifyExpiry: true,
		ExpiresAt: at(-1),
	})
}

// seedTrafficUser 造一个开了流量偏好、用量达 usedPct% 的用户；warnPercent
// 传越界值（>100）走回落口径。
func seedTrafficUser(t *testing.T, db *gorm.DB, warnPercent int, usedPct int64) storage.User {
	t.Helper()
	gb := int64(1) << 30
	u := seedUser(t, db, storage.User{
		Username: "tr-err", SubToken: "q1", QuotaBytes: gb, ResetCycle: "none",
		Enabled: true, NotifyTraffic: true, TrafficWarnPercent: warnPercent,
	})
	now := time.Now()
	if err := db.Create(&storage.TrafficLog{
		UserID: u.ID, RxBytes: gb * usedPct / 100, RecordedAt: now,
	}).Error; err != nil {
		t.Fatalf("traffic seed: %v", err)
	}
	return u
}

// TestSweepExpiryDBErrors 到期扫描三类故障：候选查询失败、同日去重查询失败、
// 站内信落库失败。
func TestSweepExpiryDBErrors(t *testing.T) {
	// 候选用户查询失败。
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want users query error")
	}

	// 同日去重查询失败（候选用户存在，通知表缺失）。
	db = newTestDB(t)
	seedExpiringUser(t, db)
	dropTable(t, db, &storage.Notification{})
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want dedup query error")
	}

	// 站内信落库失败。
	db = newTestDB(t)
	seedExpiringUser(t, db)
	failCreates(t, db)
	if err := sweepExpiry(db, time.Now()); err == nil {
		t.Fatal("want notification create error")
	}
}

// TestExpiryTextFutureDays 未来到期文案走「N 天后到期」分支。
func TestExpiryTextFutureDays(t *testing.T) {
	now := time.Now()
	text := expiryText(now.Add(3*24*time.Hour), now)
	if !strings.Contains(text, "3 天后到期") {
		t.Fatalf("text = %q, want 3 天后到期", text)
	}
}

// TestSweepTrafficDBErrors 流量扫描五类口径：候选查询失败、去重查询失败、
// 用量查询失败、阈值越界回落 80、站内信落库失败。
func TestSweepTrafficDBErrors(t *testing.T) {
	// 候选用户查询失败。
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want users query error")
	}

	// 同日去重查询失败。
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	dropTable(t, db, &storage.Notification{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want dedup query error")
	}

	// 用量查询失败（流量表缺失）。
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	dropTable(t, db, &storage.TrafficLog{})
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want usage query error")
	}

	// 阈值越界回落 80：用量 90% 仍预警。
	db = newTestDB(t)
	seedTrafficUser(t, db, 200, 90)
	if err := sweepTraffic(db, time.Now()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var rows []storage.Notification
	if err := db.Where("type = ?", storage.NotifTraffic).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// 阈值未回落（warn=200）时 89% 不预警，故有通知即证明走了回落。
	if len(rows) != 1 || !strings.Contains(rows[0].Title, "流量预警") {
		t.Fatalf("traffic notifications = %d, want one clamped warn", len(rows))
	}

	// 站内信落库失败。
	db = newTestDB(t)
	seedTrafficUser(t, db, 80, 90)
	failCreates(t, db)
	if err := sweepTraffic(db, time.Now()); err == nil {
		t.Fatal("want notification create error")
	}
}

// TestLoopLogsSweepError 循环内扫描失败只记日志、不退出。
func TestLoopLogsSweepError(t *testing.T) {
	db := newTestDB(t)
	dropTable(t, db, &storage.User{})

	l := &bufLogger{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 5*time.Millisecond, log.New(l, "", 0))
		close(done)
	}()
	waitForLog(t, "notifyscan:", l)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
