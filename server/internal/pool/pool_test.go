package pool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newDB opens an in-memory database and migrates it.
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
		// GORM skips writing zero-value booleans that have a default; disabling needs an explicit update.
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

// TestSweepSuspendAndResume covers the main path: 3 consecutive sick → suspend;
// then 2 consecutive healthy → resume; short of the threshold or mixed verdicts leave the state alone.
func TestSweepSuspendAndResume(t *testing.T) {
	db := newDB(t)
	sick := seedNode(t, db, "sick-entry", "entry", true)
	mixed := seedNode(t, db, "mixed-entry", "entry", true)

	// sick-entry: three consecutive sick → suspend
	for _, age := range []time.Duration{9 * time.Minute, 6 * time.Minute, 3 * time.Minute} {
		report(t, db, sick.ID, agentproto.ProbeVerdictSick, age)
	}
	// mixed-entry: two sick + one healthy, not 3 consecutive sick
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

	// sweep another round while suspended (verdicts unchanged) → no repeated transition
	events, err = Sweep(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("idempotent sweep emitted %+v", events)
	}

	// Recovery: one healthy is short of the threshold; the second one resumes
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

// TestSweepOnceEmitsNodeDown covers single-node suspend/resume events (HERALD-3 slack):
// automatic suspension emits node_down (warning); resume and repeat sweeps do not re-emit.
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
	// sweep again while suspended: no re-emit
	if err := sweepOnce(db, log.New(&bytes.Buffer{}, "", 0)); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&storage.Event{}).Where("kind = ?", "node_down").Count(&cnt)
	if cnt != 1 {
		t.Fatalf("second sweep events = %d, want 1", cnt)
	}
}

// TestSweepScope covers scope: only enabled entry/both nodes take part; stale verdicts
// do not count; suspension needs the full quota of sick.
func TestSweepScope(t *testing.T) {
	db := newDB(t)
	landing := seedNode(t, db, "landing-1", "landing", true)
	disabled := seedNode(t, db, "disabled-entry", "entry", false)
	stale := seedNode(t, db, "stale-entry", "entry", true)

	for i := 0; i < DefaultSickStrikes; i++ {
		report(t, db, landing.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
		report(t, db, disabled.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
	}
	// stale-entry: three sick but all stale
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

// TestResume covers manual resume: a suspended node resumes with a trace; active reports ErrNotSuspended.
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

// TestSuspend covers manual suspend (E-25): out of the pool at once with a trace; re-suspend
// reports already-suspended, non-entry nodes are rejected, unknown nodes report not-found.
func TestSuspend(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "manual-entry", "entry", true)
	other := seedNode(t, db, "relay-only", "relay", true)

	got, err := Suspend(db, n.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.PoolState != StateSuspended || got.PoolReason != "手动摘除" || got.PoolChangedAt == nil {
		t.Fatalf("suspended = %+v", got)
	}
	if _, err := Suspend(db, n.ID, nil); !errors.Is(err, ErrAlreadySuspended) {
		t.Fatalf("re-suspend = %v; want ErrAlreadySuspended", err)
	}
	if _, err := Suspend(db, other.ID, nil); err == nil {
		t.Fatal("non-entry node suspend should error")
	}
	if _, err := Suspend(db, 424242, nil); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing node suspend = %v; want record not found", err)
	}
}

// TestLoopSweepsAndStopsOnCancel covers Loop start/stop: one round runs on start (the
// consecutively-sick node is suspended), exits after ctx cancel; nil logger falls back to the default.
func TestLoopSweepsAndStopsOnCancel(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "sick-entry", "entry", true)
	for _, age := range []time.Duration{9 * time.Minute, 6 * time.Minute, 3 * time.Minute} {
		report(t, db, n.ID, agentproto.ProbeVerdictSick, age)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if getPoolState(t, db, n.ID).PoolState == StateSuspended {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop first sweep never suspended sick node")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel")
	}
}

// TestMarkStandbyBranches covers standby-mark branches: missing node / non-entry role /
// not active in the pool each error out clearly; a normal entry marks standby fine, re-marking reports ErrNotActive.
func TestMarkStandbyBranches(t *testing.T) {
	db := newDB(t)
	if _, err := MarkStandby(db, 999); err == nil {
		t.Fatal("missing node must fail")
	}
	landing := seedNode(t, db, "landing-1", "landing", true)
	if _, err := MarkStandby(db, landing.ID); err == nil ||
		!strings.Contains(err.Error(), "not an entry") {
		t.Fatalf("err = %v; want not an entry", err)
	}
	n := seedNode(t, db, "entry-1", "entry", true)
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
		Update("pool_state", StateActive).Error; err != nil {
		t.Fatal(err)
	}
	got, err := MarkStandby(db, n.ID)
	if err != nil || got.PoolState != StateStandby {
		t.Fatalf("mark = %+v err=%v", got, err)
	}
	if _, err := MarkStandby(db, n.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("re-mark err = %v; want ErrNotActive", err)
	}
}

// TestPromote covers standby promotion: the default reason applies when empty; an explicit reason is kept.
func TestPromote(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "spare-1", "entry", true)
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
		Update("pool_state", StateStandby).Error; err != nil {
		t.Fatal(err)
	}
	if err := Promote(db, n.ID, ""); err != nil {
		t.Fatal(err)
	}
	var got storage.Node
	if err := db.First(&got, n.ID).Error; err != nil || got.PoolState != StateActive ||
		got.PoolReason != "预备补位" {
		t.Fatalf("promoted = %+v err=%v", got, err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).
		Update("pool_state", StateStandby).Error; err != nil {
		t.Fatal(err)
	}
	if err := Promote(db, n.ID, "L2 补位"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&got, n.ID).Error; err != nil || got.PoolReason != "L2 补位" {
		t.Fatalf("custom reason = %+v err=%v", got, err)
	}
}

// TestAnnounceWebhook covers pool event delivery: with a webhook configured, announce sends
// synchronously; a channel failure only logs and never blocks.
func TestAnnounceWebhook(t *testing.T) {
	db := newDB(t)
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
	}))
	defer srv.Close()
	if err := storage.SetSetting(db, "notify_webhook_url", srv.URL); err != nil {
		t.Fatal(err)
	}
	ev := Event{NodeID: 1, Name: "entry-1", Action: "manual_suspend", Reason: "管理员操作"}
	announce(db, log.New(&bytes.Buffer{}, "", 0), ev)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("webhook calls = %d; want 1", n)
	}

	// Channel down: Send errors are only logged.
	srv.Close()
	var buf bytes.Buffer
	announce(db, log.New(&buf, "", 0), ev)
	if !strings.Contains(buf.String(), "notify webhook") {
		t.Fatalf("log = %q; want send failure logged", buf.String())
	}
}
