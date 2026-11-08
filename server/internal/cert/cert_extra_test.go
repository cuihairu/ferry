package cert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestLoopDefaultsIntervalAndSweepError interval<=0 回落缺省；库不可用时
// Sweep 原样上抛，Loop 记日志继续下一轮。
func TestLoopDefaultsIntervalAndSweepError(t *testing.T) {
	db := newDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	Loop(ctx, db, nil, nil, 0, log.New(&buf, "", 0))
	if !strings.Contains(buf.String(), "cert sweep") {
		t.Fatalf("sweep error not logged: %q", buf.String())
	}
}

// TestSweepExpiryReadErrors 签发/续期成功但到期读不出：不回滚签发，记日志
// 保留空到期，下轮续期窗口判定跳过。
func TestSweepExpiryReadErrors(t *testing.T) {
	db := newDB(t)
	rr := &recRunner{}
	m := &Manager{Home: "/tmp/acme", Webroot: "/tmp/www", Run: rr.run,
		ReadExpiry: func(string) (time.Time, error) { return time.Time{}, errors.New("no cert") }}
	mkTask(t, db, func(x *storage.CertTask) { x.Method = MethodHTTP01 })
	na := time.Now().Add(3 * 24 * time.Hour) // 已进 30 天续期窗口
	mkTask(t, db, func(x *storage.CertTask) {
		x.Method = MethodHTTP01
		x.State = StateOK
		x.NotAfter = &na
	})

	var buf bytes.Buffer
	if err := Sweep(context.Background(), db, m, nil, time.Now(), log.New(&buf, "", 0)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "cert expiry read"); n != 2 {
		t.Fatalf("expiry read logged %d times; want 2 (issue+renew): %q", n, buf.String())
	}
}

// TestSweepExpiringDedupHit 临期事件当日已发过：跳过不再落第二条。
func TestSweepExpiringDedupHit(t *testing.T) {
	db := newDB(t)
	rr := &recRunner{}
	m := newMgr(rr, time.Now().Add(30*24*time.Hour))
	task := mkTask(t, db, func(x *storage.CertTask) {
		x.Method = MethodHTTP01
		x.State = StateOK
		na := time.Now().Add(3 * 24 * time.Hour)
		x.NotAfter = &na
	})
	key := fmt.Sprintf("cert:%s:cert_expiring:%s", task.Domain, time.Now().Format("20060102"))
	now := time.Now()
	if err := db.Create(&storage.Event{Kind: herald.KindCertExpiring, Severity: "warning",
		Title: "t", Target: "admin", DedupKey: key, OccurredAt: now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}

	if err := Sweep(context.Background(), db, m, nil, time.Now(), log.New(io.Discard, "", 0)); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Model(&storage.Event{}).Where("kind = ?", herald.KindCertExpiring).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("cert_expiring events = %d; want only the seeded one", n)
	}
}

// TestSweepDomainsSans 签发命令带附加域名：空白逐个剥掉，空的跳过。
func TestSweepDomainsSans(t *testing.T) {
	db := newDB(t)
	rr := &recRunner{}
	m := &Manager{Home: "/tmp/acme", Webroot: "/tmp/www", Run: rr.run,
		ReadExpiry: func(string) (time.Time, error) { return time.Now(), nil }}
	mkTask(t, db, func(x *storage.CertTask) {
		x.Method = MethodHTTP01
		x.Sans = " a.example.com , b.example.com "
	})
	if err := Sweep(context.Background(), db, m, nil, time.Now(), log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) == 0 || !strings.Contains(rr.calls[0], "-d a.example.com -d b.example.com") {
		t.Fatalf("issue args = %q; want sans as -d sequence", rr.calls)
	}
}

// TestTailTruncates 尾段截断：长串取尾部 n 字节，短串原样。
func TestTailTruncates(t *testing.T) {
	if got := tail("short", 200); got != "short" {
		t.Fatalf("tail short = %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := tail(long, 200); got != long[100:] {
		t.Fatalf("tail long length = %d; want last 200 bytes", len(got))
	}
}
