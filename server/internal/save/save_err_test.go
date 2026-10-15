package save

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 二十三批补口：save 各错误返回口径（水位读取/清空重建/流量查询/累加/水位
// 回写）与循环内扫描失败日志。

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

// drop 拆表：后续针对该表的语句报 no such table。
func drop(t *testing.T, db *gorm.DB, dest any) {
	t.Helper()
	if err := db.Migrator().DropTable(dest); err != nil {
		t.Fatalf("drop table: %v", err)
	}
}

// failSettingWrites 让水位设置键的写入失败（Save 可能走 update 或 create 链）。
func failSettingWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	f := func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	}
	db.Callback().Update().Before("gorm:update").Register("test:fail_setting_update", f)
	db.Callback().Create().Before("gorm:create").Register("test:fail_setting_create", f)
}

// setWatermark 预置水位键（跳过无水位清空重建分支）。
func setWatermark(t *testing.T, db *gorm.DB, v int64) {
	t.Helper()
	if err := storage.SetSetting(db, settingWatermark, strconv.FormatInt(v, 10)); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
}

func traffic1() storage.NodeTrafficLog {
	return storage.NodeTrafficLog{
		NodeID: 1, DirectBytes: 100,
		RecordedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
	}
}

// TestSweepDBErrors Sweep 五类库故障：水位读取失败、无水位清空失败、流量
// 查询失败、累加失败、水位回写失败。
func TestSweepDBErrors(t *testing.T) {
	// 水位读取失败。
	db := newTestDB(t)
	drop(t, db, &storage.Setting{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want watermark read error")
	}

	// 无水位清空重建失败。
	db = newTestDB(t)
	drop(t, db, &storage.SaveStat{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want delete error")
	}

	// 流量查询失败。
	db = newTestDB(t)
	drop(t, db, &storage.NodeTrafficLog{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want traffic query error")
	}

	// 累加失败：水位已置（跳过清空），流量行已读，save_stats 缺失。
	db = newTestDB(t)
	setWatermark(t, db, 0)
	seedTraffic(t, db, traffic1())
	drop(t, db, &storage.SaveStat{})
	if _, err := Sweep(db); err == nil {
		t.Fatal("want add error")
	}

	// 水位回写失败。
	db = newTestDB(t)
	seedTraffic(t, db, traffic1())
	failSettingWrites(t, db)
	if _, err := Sweep(db); err == nil {
		t.Fatal("want watermark write error")
	}
}

// TestUserSavingsDBErrors 折算两条查询各自的失败口径。
func TestUserSavingsDBErrors(t *testing.T) {
	db := newTestDB(t)
	drop(t, db, &storage.SaveStat{})
	if _, _, _, err := UserSavings(db, 1, "2026-10-01"); err == nil {
		t.Fatal("want save_stats query error")
	}

	db = newTestDB(t)
	drop(t, db, &storage.TrafficLog{})
	if _, _, _, err := UserSavings(db, 1, "2026-10-01"); err == nil {
		t.Fatal("want traffic_logs query error")
	}
}

// TestLoopLogsSweepError 循环内扫描失败只记日志、不退出。
func TestLoopLogsSweepError(t *testing.T) {
	db := newTestDB(t)
	drop(t, db, &storage.SaveStat{}) // 无水位 + 表缺失：每轮清空重建失败

	buf := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 5*time.Millisecond, log.New(buf, "", 0))
		close(done)
	}()
	waitForLog(t, "save stats sweep:", buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop on cancel")
	}
}
