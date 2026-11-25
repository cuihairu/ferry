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

// TestLoopDefaultsIntervalAndSweepError interval<=0 falls back to the default; when the DB is unavailable
// Sweep propagates as-is and Loop logs the error and continues with the next round.
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

// TestSweepExpiryReadErrors issuance/renewal succeeded but the expiry cannot be read: no rollback of the
// issuance, an error is logged, the empty expiry is kept, and the next round's renewal window check skips it.
func TestSweepExpiryReadErrors(t *testing.T) {
	db := newDB(t)
	rr := &recRunner{}
	m := &Manager{Home: "/tmp/acme", Webroot: "/tmp/www", Run: rr.run,
		ReadExpiry: func(string) (time.Time, error) { return time.Time{}, errors.New("no cert") }}
	mkTask(t, db, func(x *storage.CertTask) { x.Method = MethodHTTP01 })
	na := time.Now().Add(3 * 24 * time.Hour) // already inside the 30-day renewal window
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

// TestSweepExpiringDedupHit the expiring notice already went out today: skip, no second row.
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

// TestSweepDomainsSans the issuance command carries additional domains: blanks are trimmed one by one, empties skipped.
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

// TestTailTruncates tail truncation: a long string takes the last n bytes, a short string is kept as-is.
func TestTailTruncates(t *testing.T) {
	if got := tail("short", 200); got != "short" {
		t.Fatalf("tail short = %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := tail(long, 200); got != long[100:] {
		t.Fatalf("tail long length = %d; want last 200 bytes", len(got))
	}
}
