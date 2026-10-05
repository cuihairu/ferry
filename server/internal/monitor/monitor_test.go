package monitor

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/aggregate"
	"github.com/cuihairu/ferry/server/internal/storage"
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
	// 首次 failed：建状态灯记录并发一条合并告警
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

	// 重复 failed：状态未迁移，不重复告警
	buf.Reset()
	if err := applyVerdict(db, logger, failed); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if buf.String() != "" {
		t.Fatalf("must not re-alarm on unchanged state: %q", buf.String())
	}

	// 恢复 healthy：状态迁移并记恢复日志
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
}

func TestSweepPersistsDimensions(t *testing.T) {
	db := openTestDB(t)
	logger, _ := newLogger()
	now := time.Now()

	// 华东 4 节点 2 sick → 区域故障；电信 5 节点 2 sick → 运营商故障
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
		"region/华东": StateFailed,   // 2/4 sick 达故障口径
		"region/华北": StateDegraded, // 1/3 sick 未达故障口径但确有异常
		"isp/电信":    StateFailed,   // 2/5 sick 达故障口径
		"isp/联通":    StateFailed,   // 1/2 占比达 50%
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

// TestRunStops 验证 Run 随 ctx 取消退出，不泄漏 goroutine。
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
