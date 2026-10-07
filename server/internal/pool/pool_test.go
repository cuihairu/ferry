package pool

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newDB 建内存库并迁移。
func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, name, role string, enabled bool) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Role: role, Enabled: true}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	if !enabled {
		// GORM 对带 default 的布尔零值会跳过写入，禁用需显式更新。
		if err := db.Model(&n).Update("enabled", false).Error; err != nil {
			t.Fatal(err)
		}
	}
	return n
}

func report(t *testing.T, db *gorm.DB, target uint, verdict string, age time.Duration) {
	t.Helper()
	nodeID := &target
	if err := db.Create(&storage.ProbeReport{
		NodeID: 999, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: nodeID,
		Verdict: verdict, ProbedAt: time.Now().Add(-age),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func getPoolState(t *testing.T, db *gorm.DB, id uint) storage.Node {
	t.Helper()
	var n storage.Node
	if err := db.First(&n, id).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSweepSuspendAndResume 覆盖主链路：连续 3 次 sick → 摘除；
// 再连续 2 次 healthy → 复位；未足额或结论混杂不动状态。
func TestSweepSuspendAndResume(t *testing.T) {
	db := newDB(t)
	sick := seedNode(t, db, "sick-entry", "entry", true)
	mixed := seedNode(t, db, "mixed-entry", "entry", true)

	// sick-entry：三条连续 sick → 摘除
	for _, age := range []time.Duration{9 * time.Minute, 6 * time.Minute, 3 * time.Minute} {
		report(t, db, sick.ID, agentproto.ProbeVerdictSick, age)
	}
	// mixed-entry：两条 sick + 一条 healthy，不构成连续 3 次 sick
	report(t, db, mixed.ID, agentproto.ProbeVerdictSick, 9*time.Minute)
	report(t, db, mixed.ID, agentproto.ProbeVerdictSick, 6*time.Minute)
	report(t, db, mixed.ID, agentproto.ProbeVerdictHealthy, 3*time.Minute)

	events, err := Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].NodeID != sick.ID || events[0].Action != "suspend" {
		t.Fatalf("events = %+v", events)
	}
	if n := getPoolState(t, db, sick.ID); n.PoolState != StateSuspended || n.PoolReason == "" || n.PoolChangedAt == nil {
		t.Fatalf("sick-entry = %+v", n)
	}
	if n := getPoolState(t, db, mixed.ID); n.PoolState != StateActive {
		t.Fatalf("mixed-entry should stay active, got %s", n.PoolState)
	}

	// 已摘除再扫一轮（结论不变）→ 无重复迁移
	events, err = Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("idempotent sweep emitted %+v", events)
	}

	// 恢复：一条 healthy 不足额，第二条才复位
	report(t, db, sick.ID, agentproto.ProbeVerdictHealthy, 3*time.Minute)
	events, err = Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("single healthy should not resume: %+v", events)
	}
	report(t, db, sick.ID, agentproto.ProbeVerdictHealthy, 1*time.Minute)
	events, err = Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "resume" || events[0].NodeID != sick.ID {
		t.Fatalf("resume events = %+v", events)
	}
	if n := getPoolState(t, db, sick.ID); n.PoolState != StateActive {
		t.Fatalf("sick-entry should be active again, got %s", n.PoolState)
	}
}

// TestSweepOnceEmitsNodeDown 覆盖单点摘挂事件（HERALD-3 余量）：自动摘除
// 落 node_down（warning），复位与重复扫不重发。
func TestSweepOnceEmitsNodeDown(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "down-entry", "entry", true)
	for _, age := range []time.Duration{9 * time.Minute, 6 * time.Minute, 3 * time.Minute} {
		report(t, db, n.ID, agentproto.ProbeVerdictSick, age)
	}
	if err := sweepOnce(db, log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", "node_down").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Severity != "warning" || evs[0].Target != "admin" ||
		evs[0].DedupKey != fmt.Sprintf("node:%d:node_down", n.ID) {
		t.Fatalf("node_down events = %+v", evs)
	}
	// 已摘除再扫：不重发
	if err := sweepOnce(db, log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&storage.Event{}).Where("kind = ?", "node_down").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("second sweep events = %d, want 1", cnt)
	}
}

// TestSweepScope 覆盖范围口径：仅 entry/both 且 enabled 的节点参与；
// 超龄结论不参与判定；足额 sick 才动手。
func TestSweepScope(t *testing.T) {
	db := newDB(t)
	landing := seedNode(t, db, "landing-1", "landing", true)
	disabled := seedNode(t, db, "disabled-entry", "entry", false)
	stale := seedNode(t, db, "stale-entry", "entry", true)

	for i := 0; i < DefaultSickStrikes; i++ {
		report(t, db, landing.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
		report(t, db, disabled.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
	}
	// stale-entry：三条 sick 但全部超龄
	for i := 0; i < DefaultSickStrikes; i++ {
		report(t, db, stale.ID, agentproto.ProbeVerdictSick, ProbeFresh+time.Duration(i+1)*time.Minute)
	}

	events, err := Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("out-of-scope nodes must be untouched, events = %+v", events)
	}
	if n := getPoolState(t, db, landing.ID); n.PoolState != StateActive {
		t.Fatal("landing node must not be pooled")
	}
	if n := getPoolState(t, db, stale.ID); n.PoolState != StateActive {
		t.Fatal("stale verdicts must not suspend")
	}
}

// TestResume 覆盖手动复位：摘除态可复位并留痕；active 态报 ErrNotSuspended。
func TestResume(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "manual", "entry", true)
	if _, err := Resume(db, n.ID, nil); !errors.Is(err, ErrNotSuspended) {
		t.Fatalf("active node resume = %v", err)
	}
	if err := setPoolState(db, n.ID, StateSuspended, "连续 3 次探测 sick"); err != nil {
		t.Fatal(err)
	}
	got, err := Resume(db, n.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.PoolState != StateActive || got.PoolReason != "手动复位" || got.PoolChangedAt == nil {
		t.Fatalf("resumed = %+v", got)
	}
	if _, err := Resume(db, 424242, nil); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing node resume = %v", err)
	}
}
