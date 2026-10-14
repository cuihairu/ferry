package review

import (
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十二批补口：review 库故障冒错、循环内错误只记日志、停用后外通道
// 故障容错口径。

// openDB 建文件库并迁移（与既有内联建库口径一致）。
func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// closeSQL 关掉底层连接，之后库操作一律报错。
func closeSQL(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

// bufLogger 线程安全日志缓冲（Run 在独立 goroutine 写）。
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

// withDefaultLog 把默认 logger 指向缓冲（Run 无 logger 注入点，只有
// log.Printf；用例间无并行，用毕恢复 stderr）。
func withDefaultLog(t *testing.T, l *bufLogger) {
	t.Helper()
	log.SetOutput(l)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
}

// waitForLog 轮询等待日志出现期望串（避免 sleep 卡死用例）。
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

// TestDisableInactiveDBError 首查即失败：不落停用数、直接冒错。
func TestDisableInactiveDBError(t *testing.T) {
	db := openDB(t)
	closeSQL(t, db)
	if n, err := DisableInactive(db, time.Now()); err == nil {
		t.Fatalf("want error, got n=%d", n)
	}
}

// TestDisableInactiveOverQuotaError 到期停用成功、超限判定失败即冒错
// （拆流量表：超限子查询引用它）。
func TestDisableInactiveOverQuotaError(t *testing.T) {
	db := openDB(t)
	past := time.Now().Add(-24 * time.Hour)
	if err := db.Create(&storage.User{
		Username: "expired", SubToken: "t-err", Enabled: true, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Migrator().DropTable(&storage.TrafficLog{}); err != nil {
		t.Fatalf("drop traffic_logs: %v", err)
	}
	if _, err := DisableInactive(db, time.Now()); err == nil {
		t.Fatal("want error from over-quota pass")
	}
}

// TestRunLogsDisableError 循环内停用失败只记日志、不退出循环。
func TestRunLogsDisableError(t *testing.T) {
	db := openDB(t)
	closeSQL(t, db)
	l := &bufLogger{}
	withDefaultLog(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 5*time.Millisecond)
		close(done)
	}()
	waitForLog(t, "review users:", l)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}

// TestRunLogsNotifyFailure 停用计数大于零且外通道故障：告警只记日志，
// 停用动作本身已生效。
func TestRunLogsNotifyFailure(t *testing.T) {
	db := openDB(t)
	past := time.Now().Add(-24 * time.Hour)
	if err := db.Create(&storage.User{
		Username: "expired", SubToken: "t-notify", Enabled: true, ExpiresAt: &past,
	}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := storage.SetSetting(db, notify.KeyURL, "http://"+deadAddr(t)); err != nil {
		t.Fatalf("set webhook: %v", err)
	}

	l := &bufLogger{}
	withDefaultLog(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 5*time.Millisecond)
		close(done)
	}()
	waitForLog(t, "notify webhook:", l)
	if !strings.Contains(l.String(), "disabled 1") {
		t.Fatalf("log lacks disabled count: %s", l.String())
	}
	cancel()
	<-done
}
