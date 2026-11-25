package monitor

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/aggregate"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
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

func newLogger() (*log.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return log.New(buf, "", 0), buf
}

func seed(t *testing.T, db *gorm.DB, target *uint, kind, region, isp, verdict string, at time.Time) {
	t.Helper()
	r := storage.ProbeReport{
		NodeID:       99,
		TargetKind:   kind,
		TargetNodeID: target,
		Direction:    agentproto.DirectionOut,
		Verdict:      verdict,
		Region:       region,
		ISP:          isp,
		ProbedAt:     at,
	}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed probe report: %v", err)
	}
}

func TestApplyVerdictTransitions(t *testing.T) {
	db := openTestDB(t)
	logger, buf := newLogger()

	failed := aggregate.Verdict{
		Dimension: aggregate.DimensionRegion, Scope: "华东",
		Sick: 2, Total: 4, SickNodes: []uint64{2, 3}, Failed: true,
	}
	// First failed: creates the status-lamp record and sends one merged alert
	if err := applyVerdict(db, logger, failed); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var row storage.DimensionStatus
	if err := db.Where("scope=? AND key=?", "region", "华东").First(&row).Error; err != nil {
		t.Fatalf("load status: %v", err)
	}
	if row.State != StateFailed || !strings.Contains(row.Reason, "2/4 节点异常") {
		t.Fatalf("status row: %+v", row)
	}
	if !strings.Contains(buf.String(), "alarm") || !strings.Contains(buf.String(), "华东") {
		t.Fatalf("merged alarm missing: %q", buf.String())
	}
	// Fault transition lands in the event outbox (HERALD-3): one critical region_fault
	var ev storage.Event
	if err := db.Where("kind = ?", "region_fault").First(&ev).Error; err != nil {
		t.Fatalf("region_fault event missing: %v", err)
	}
	if ev.Severity != "critical" || ev.DedupKey != "region:华东:region_fault" || ev.Target != "admin" {
		t.Fatalf("event = %+v", ev)
	}

	// Repeated failed: no state transition, no repeated alert, no repeated event
	buf.Reset()
	if err := applyVerdict(db, logger, failed); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if buf.String() != "" {
		t.Fatalf("must not re-alarm on unchanged state: %q", buf.String())
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "region_fault").Count(&n)
	if n != 1 {
		t.Fatalf("region_fault events = %d, want 1", n)
	}

	// Recovering to healthy: state transition and a recovery log line
	healthy := aggregate.Verdict{
		Dimension: aggregate.DimensionRegion, Scope: "华东",
		Sick: 0, Total: 4, Failed: false,
	}
	if err := applyVerdict(db, logger, healthy); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := db.Where("scope=? AND key=?", "region", "华东").First(&row).Error; err != nil {
		t.Fatalf("load status: %v", err)
	}
	if row.State != StateHealthy || row.Since.IsZero() {
		t.Fatalf("status row after recovery: %+v", row)
	}
	if !strings.Contains(buf.String(), "recovered") {
		t.Fatalf("recovery log missing: %q", buf.String())
	}
}

func TestApplyVerdictDegraded(t *testing.T) {
	db := openTestDB(t)
	logger, buf := newLogger()

	degraded := aggregate.Verdict{
		Dimension: aggregate.DimensionISP, Scope: "电信",
		Sick: 1, Total: 5, Failed: false,
	}
	if err := applyVerdict(db, logger, degraded); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var row storage.DimensionStatus
	if err := db.Where("scope=? AND key=?", "isp", "电信").First(&row).Error; err != nil {
		t.Fatalf("load status: %v", err)
	}
	if row.State != StateDegraded {
		t.Fatalf("state = %q, want degraded", row.State)
	}
	if !strings.Contains(buf.String(), "degraded") {
		t.Fatalf("degraded log missing: %q", buf.String())
	}
	// Degraded is not among the three alert kinds: no event is stored
	var n int64
	db.Model(&storage.Event{}).Count(&n)
	if n != 0 {
		t.Fatalf("degraded must not emit events, got %d", n)
	}
}

func TestSweepPersistsDimensions(t *testing.T) {
	db := openTestDB(t)
	logger, _ := newLogger()
	now := time.Now()

	// 4 nodes / 2 sick → region fault; 5 nodes / 2 sick → ISP fault
	n1, n2, n3, n4, n5, n6, n7 := uint(1), uint(2), uint(3), uint(4), uint(5), uint(6), uint(7)
	seed(t, db, &n1, agentproto.ProbeTargetTunnel, "华东", "电信", agentproto.ProbeVerdictHealthy, now.Add(-1*time.Minute))
	seed(t, db, &n2, agentproto.ProbeTargetTunnel, "华东", "电信", agentproto.ProbeVerdictSick, now.Add(-1*time.Minute))
	seed(t, db, &n3, agentproto.ProbeTargetTunnel, "华东", "联通", agentproto.ProbeVerdictSick, now.Add(-1*time.Minute))
	seed(t, db, &n4, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictHealthy, now.Add(-1*time.Minute))
	seed(t, db, &n5, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictSick, now.Add(-1*time.Minute))
	seed(t, db, &n6, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictHealthy, now.Add(-1*time.Minute))
	seed(t, db, &n7, agentproto.ProbeTargetTunnel, "华东", "联通", agentproto.ProbeVerdictHealthy, now.Add(-1*time.Minute))

	if err := sweep(db, logger); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	var rows []storage.DimensionStatus
	if err := db.Order("scope, key").Find(&rows).Error; err != nil {
		t.Fatalf("load statuses: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Scope+"/"+r.Key] = r.State
	}
	want := map[string]string{
		"region/华东": StateFailed,   // 2/4 sick meets the fault criteria
		"region/华北": StateDegraded, // 1/3 sick falls short of the fault criteria but is clearly abnormal
		"isp/电信":    StateFailed,   // 2/5 sick meets the fault criteria
		"isp/联通":    StateFailed,   // 1/2 ratio reaches 50%
	}
	if len(got) != len(want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	for k, state := range want {
		if got[k] != state {
			t.Fatalf("status %s = %q, want %q", k, got[k], state)
		}
	}
}

// TestRunStops verifies Run exits on ctx cancel without leaking a goroutine.
func TestRunStops(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, db, 10*time.Millisecond, log.New(&bytes.Buffer{}, "", 0))
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

// TestApplyVerdictNotifies covers all three transition tiers being sent outbound once a webhook is configured:
// failed / degraded / recovered, one each; Send failures only log and never block.
func TestApplyVerdictNotifies(t *testing.T) {
	db := openTestDB(t)
	// Already-closed server address: connections are refused immediately, the error takes the log branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u := srv.URL
	srv.Close()
	if err := storage.SetSetting(db, notify.KeyURL, u); err != nil {
		t.Fatal(err)
	}
	logger, buf := newLogger()

	failed := aggregate.Verdict{Dimension: aggregate.DimensionRegion, Scope: "华北",
		Sick: 3, Total: 3, Failed: true}
	if err := applyVerdict(db, logger, failed); err != nil {
		t.Fatal(err)
	}
	degraded := aggregate.Verdict{Dimension: aggregate.DimensionISP, Scope: "联通",
		Sick: 1, Total: 6}
	if err := applyVerdict(db, logger, degraded); err != nil {
		t.Fatal(err)
	}
	healthy := aggregate.Verdict{Dimension: aggregate.DimensionRegion, Scope: "华北"}
	if err := applyVerdict(db, logger, healthy); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"notify webhook", "notify webhook"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("webhook error log missing: %q", buf.String())
		}
	}
}

// TestSweepUnmigratedDB covers Judge's table lookup failing on an unmigrated DB: sweep surfaces the
// error; Run logs periodically without panicking and exits after cancel.
func TestSweepUnmigratedDB(t *testing.T) {
	dsn := fmt.Sprintf("file:monitor-raw-%d?mode=memory&cache=shared", time.Now().UnixNano())
	raw, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	logger, buf := newLogger()
	if err := sweep(raw, logger); err == nil {
		t.Fatal("sweep on unmigrated db should error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, raw, 5*time.Millisecond, logger)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if !strings.Contains(buf.String(), "dimension sweep") {
		t.Fatalf("sweep error log missing: %q", buf.String())
	}
}
