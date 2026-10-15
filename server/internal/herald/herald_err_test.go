package herald

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十三批补口：herald 各错误返回口径——发送侧（URL 解析/拨号）、投递侧
// 事务失败、入库与计数查询失败、循环内错误日志。
// http.go 的载荷 marshal 失败分支留白：outgoingEvent 字段全是字符串/整数/
// 时间与 RawMessage，正常无法构造 marshal 错误（防御性代码不硬凑）。

// syncBuf 线程安全日志缓冲（Loop 在独立 goroutine 写）。
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitForLog 轮询等待日志出现期望串。
func waitForLog(t *testing.T, want string, s *syncBuf) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.String(), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("log lacks %q: %s", want, s.String())
}

// deadAddr 返回一个必被拒的本地地址（已关闭的监听端口）。
func deadAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listen: %v", err)
	}
	return addr
}

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

// failEventUpdates 让事件行的更新失败（markSent/markRetry 都在事务内 Update）。
func failEventUpdates(t *testing.T, db *gorm.DB) {
	t.Helper()
	db.Callback().Update().Before("gorm:update").Register("test:fail_event_update", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Event); ok {
			tx.AddError(errors.New("db down"))
		}
	})
}

func testEvent(t *testing.T) storage.Event {
	t.Helper()
	return storage.Event{ID: 1, Kind: "cost_exceeded", Severity: SeverityCritical,
		Title: "事件", Target: "admin", Status: StatusPending, OccurredAt: time.Now()}
}

// TestHTTPSenderRequestErrors 发送侧请求构造与拨号失败。
func TestHTTPSenderRequestErrors(t *testing.T) {
	// URL 解析失败。
	if err := HTTPSender("http://bad url", "")(testEvent(t)); err == nil {
		t.Fatal("want URL parse error")
	}
	// 拨号失败。
	if err := HTTPSender("http://"+deadAddr(t), "")(testEvent(t)); err == nil {
		t.Fatal("want dial error")
	}
}

// TestEmitCountDBErrors 入库与计数在库不可用时的冒错口径。
func TestEmitCountDBErrors(t *testing.T) {
	db := newHeraldDB(t)
	closeDB(t, db)
	if _, err := Emit(db, EmitInput{Kind: "recovery_failed", Severity: SeverityCritical,
		Title: "事件", Target: "admin"}); err == nil {
		t.Fatal("want emit create error")
	}
	if _, _, _, err := Count(db); err == nil {
		t.Fatal("want count query error")
	}
}

// TestDeliverErrorPaths 投递各错误口径：空发送器短路、到期查询失败、
// 成功置态失败、失败排程失败。
func TestDeliverErrorPaths(t *testing.T) {
	now := time.Now()

	// 空发送器：只空转返回。
	db := newHeraldDB(t)
	n, err := Deliver(db, now, nil)
	if err != nil || n != 0 {
		t.Fatalf("Deliver(nil) = %d %v", n, err)
	}

	// 到期查询失败。
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	closeDB(t, db)
	if _, err := Deliver(db, now, func(storage.Event) error { return nil }); err == nil {
		t.Fatal("want due query error")
	}

	// 成功置态失败（markSent 事务内更新失败）。
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	failEventUpdates(t, db)
	if _, err := Deliver(db, now, func(storage.Event) error { return nil }); err == nil {
		t.Fatal("want markSent error")
	}

	// 失败排程失败（sender 报错 + markRetry 事务内更新失败）。
	db = newHeraldDB(t)
	emitOne(t, db, "cost_exceeded")
	failEventUpdates(t, db)
	boom := errors.New("channel down")
	if _, err := Deliver(db, now, func(storage.Event) error { return boom }); err == nil {
		t.Fatal("want markRetry error")
	}
}

// TestLoopLogsDeliverError 循环内投递失败只记日志、不退出。
func TestLoopLogsDeliverError(t *testing.T) {
	db := newHeraldDB(t)
	closeDB(t, db) // 每轮到期查询失败

	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 5*time.Millisecond, func(storage.Event) error { return nil }, log.New(buf, "", 0))
		close(done)
	}()
	waitForLog(t, "herald deliver:", buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
