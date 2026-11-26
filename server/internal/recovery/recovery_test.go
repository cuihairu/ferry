package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

type fakeAction struct {
	name  string
	err   error
	runs  atomic.Int32
	sleep time.Duration
}

func (f *fakeAction) Name() string { return f.name }
func (f *fakeAction) Run(ctx context.Context, node storage.Node) error {
	f.runs.Add(1)
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

// suspendedNode creates an entry node that was auto-suspended and left unrestored.
func suspendedNode(t *testing.T, db *gorm.DB, name, reason string, changedAt time.Time) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Port: 443, Protocol: "vless", Enabled: true, Token: "t-" + name,
		Status: "online", Role: "entry", PoolState: "suspended", PoolReason: reason, PoolChangedAt: &changedAt}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func loadRecovery(t *testing.T, db *gorm.DB, nodeID uint) storage.Recovery {
	t.Helper()
	var rec storage.Recovery
	if err := db.Where("node_id = ?", nodeID).First(&rec).Error; err != nil {
		t.Fatalf("recovery row: %v", err)
	}
	return rec
}

// waitRecovery polls until the recovery row satisfies the condition (the action writes back in a goroutine).
func waitRecovery(t *testing.T, db *gorm.DB, nodeID uint, cond func(storage.Recovery) bool) storage.Recovery {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := loadRecovery(t, db, nodeID)
		if cond(rec) {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery never satisfied: %+v", rec)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSweepOpenAndAdvance covers verdict opening and tiered advancement: removal left
// unrestored opens a pipeline; unregistered L1/L2 advance as skipped; a registered L3 action starts; manual removal does not open one.
func TestSweepOpenAndAdvance(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	suspendedNode(t, db, "manual-1", "手动摘除", now.Add(-15*time.Minute))

	l3 := &fakeAction{name: "new_instance"}
	reg := Registry{3: l3}

	// First round: open the verdict (including the manual-removal exclusion).
	if err := Sweep(db, now, Options{}, reg, nil); err != nil {
		t.Fatal(err)
	}
	rec := loadRecovery(t, db, 1)
	if rec.State != StateRunning || rec.Level != 1 {
		t.Fatalf("opened = %+v", rec)
	}
	var manual int64
	db.Model(&storage.Recovery{}).Where("node_name = ?", "manual-1").Count(&manual)
	if manual != 0 {
		t.Fatal("manual suspend must not open recovery")
	}
	// Verdict event (HERALD-3): the opened node lands node_blocked; manual removal does not
	var blockedEv []storage.Event
	if err := db.Where("kind = ?", "node_blocked").Find(&blockedEv).Error; err != nil {
		t.Fatal(err)
	}
	if len(blockedEv) != 1 || blockedEv[0].DedupKey == "" || blockedEv[0].Severity != "critical" {
		t.Fatalf("node_blocked events = %+v", blockedEv)
	}

	// Later rounds: L1/L2 skipped advancement → L3 starts.
	for i := 0; i < 3; i++ {
		if err := Sweep(db, now, Options{}, reg, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The L3 action finishes quickly (fake); just wait until it has started (action name recorded) without relying on the running state.
	rec = waitRecovery(t, db, 1, func(r storage.Recovery) bool {
		return r.Level == 3 && r.Action == "new_instance"
	})
	if rec.ActionState != ActionRunning && rec.ActionState != ActionOK {
		t.Fatalf("action_state = %+v", rec)
	}
	// Later rounds advancing must not re-emit the verdict event
	var evN int64
	db.Model(&storage.Event{}).Where("kind = ?", "node_blocked").Count(&evN)
	if evN != 1 {
		t.Fatalf("node_blocked events after advances = %d, want 1", evN)
	}
}

// TestRecoveryDoneOnResume covers probe recovery finishing immediately: after the suspension
// reset (pool active) the pipeline closes as done, with no failure reason written.
func TestRecoveryDoneOnResume(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-2", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	if err := db.Model(&storage.Node{}).Where("id = ?", n.ID).Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 2, State: StateRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if got.State != StateDone || got.FinishedAt == nil || got.LastErr != "" {
		t.Fatalf("done = %+v", got)
	}
}

// TestTimeoutExhaustFails covers level-timeout advancement and full exhaustion:
// L3 action timeout → no next level → pipeline failed (escalation is BR-5's job).
func TestTimeoutExhaustFails(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-3", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	start := now.Add(-11 * time.Minute) // past the default LevelTimeout of 10 minutes
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 3, State: StateRunning,
		Action: "new_instance", ActionState: ActionRunning,
		LevelStartedAt: start, StartedAt: start, UpdatedAt: start}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, Registry{3: &fakeAction{name: "new_instance"}}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if got.State != StateFailed || got.LastErr == "" {
		t.Fatalf("failed = %+v", got)
	}
}

// TestActionFailedAdvances covers failing immediately into the next level and eventually
// exhausting: L3 action error → pipeline failed with the reason recorded.
func TestActionFailedAdvances(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-4", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 3, State: StateRunning,
		LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	l3 := &fakeAction{name: "new_instance", err: context.DeadlineExceeded}
	if err := Sweep(db, now, Options{}, Registry{3: l3}, nil); err != nil {
		t.Fatal(err)
	}
	// The action fails in its goroutine and writes back; wait for the DB write, then sweep once more to reach a terminal state.
	waitRecovery(t, db, n.ID, func(r storage.Recovery) bool { return r.ActionState == ActionFailed })
	if err := Sweep(db, now, Options{}, Registry{3: l3}, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, n.ID)
	if l3.runs.Load() != 1 {
		t.Fatalf("runs = %d", l3.runs.Load())
	}
	if got.State != StateFailed || got.LastErr == "" {
		t.Fatalf("failed = %+v", got)
	}
}

// TestInstanceActionL3 covers the L3 one-click new instance going through the provisioning
// pipeline: the replacement node row is pre-issued, tofu apply succeeds (fake runner injected), the job is traced ok; missing credentials fail with a reason.
func TestInstanceActionL3(t *testing.T) {
	db := newTestDB(t)
	store := secret.NewStore("recovery-master")
	sealed, err := store.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.Provider{Name: "vultr", Type: "vultr", AccessKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	tpl := storage.ProvisionTemplate{Name: "replace", ProviderID: prov.ID,
		Plan: "vc2-1c-1gb", Region: "hkg", BillingType: "包月", Direction: "out", Role: "entry"}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatal(err)
	}
	m := provision.New(db, "tofu", t.TempDir(), func(ctx context.Context, dir string, args []string, env map[string]string) (string, error) {
		return "applied", nil
	})
	a := &InstanceAction{
		Manager: m, Store: store, TemplateID: tpl.ID,
		Launch: func(name string) provision.LaunchParams {
			return provision.LaunchParams{
				InstanceName: name,
				PanelWSURL:   provision.PanelWSURL("http://panel.example.com"),
				AgentBase:    "https://dl.example.com", AgentVersion: "1.0.0",
				NewToken: func() (string, error) { return "tok-replace", nil },
			}
		},
	}
	node := storage.Node{Name: "entry-4", Port: 443, Protocol: "vless"}
	if err := a.Run(context.Background(), node); err != nil {
		t.Fatalf("L3 run: %v", err)
	}
	var got storage.Node
	if err := db.Where("name = ?", "entry-4-r").First(&got).Error; err != nil {
		t.Fatalf("replacement node: %v", err)
	}
	if got.Status != "provisioning" || got.Token != "tok-replace" {
		t.Fatalf("replacement = %+v", got)
	}
	var jobs []storage.ProvisionJob
	if err := db.Find(&jobs).Error; err != nil || len(jobs) != 1 || jobs[0].Status != "ok" {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}

	// No template configured: fails with a reason, never faking support.
	bare := &InstanceAction{Manager: m, Store: store}
	if err := bare.Run(context.Background(), node); err == nil {
		t.Fatal("unconfigured L3 must fail")
	}
}

// TestTraceRowsReplay covers action traces (BR-5): unregistered L1/L2 record skipped,
// the failed L3 action records failed with a reason; the per-level replay order is complete.
func TestTraceRowsReplay(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n := suspendedNode(t, db, "entry-5", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	l3 := &fakeAction{name: "new_instance", err: context.DeadlineExceeded}
	reg := Registry{3: l3}

	// Open + advance: L1 skipped → L2 skipped → L3 starts (the goroutine writes back an error).
	for i := 0; i < 4; i++ {
		if err := Sweep(db, now, Options{}, reg, nil); err != nil {
			t.Fatal(err)
		}
	}
	waitRecovery(t, db, n.ID, func(r storage.Recovery) bool { return r.ActionState == ActionFailed })
	if err := Sweep(db, now, Options{}, reg, nil); err != nil {
		t.Fatal(err)
	}

	var traces []storage.RecoveryAction
	if err := db.Where("recovery_id = ?", 1).Order("id ASC").Find(&traces).Error; err != nil {
		t.Fatal(err)
	}
	if len(traces) != 3 {
		t.Fatalf("traces = %+v", traces)
	}
	if traces[0].Level != 1 || traces[0].State != ActionSkipped || traces[0].Detail == "" {
		t.Fatalf("L1 trace = %+v", traces[0])
	}
	if traces[1].Level != 2 || traces[1].State != ActionSkipped {
		t.Fatalf("L2 trace = %+v", traces[1])
	}
	if traces[2].Level != 3 || traces[2].Action != "new_instance" ||
		traces[2].State != ActionFailed ||
		!strings.Contains(traces[2].Detail, "context deadline exceeded") ||
		traces[2].FinishedAt == nil {
		t.Fatalf("L3 trace = %+v", traces[2])
	}
}

// TestFailedEscalationNotify covers failure escalation (BR-5): exhaustion lands the terminal
// failed state and sends one escalation alert over the notify channel; in-flight trace rows close out as timeout.
func TestFailedEscalationNotify(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	var mu sync.Mutex
	var events []notify.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e notify.Event
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &e)
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := storage.SetSetting(db, notify.KeyURL, srv.URL); err != nil {
		t.Fatal(err)
	}

	n := suspendedNode(t, db, "entry-6", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	start := now.Add(-11 * time.Minute) // past the default LevelTimeout
	rec := storage.Recovery{NodeID: n.ID, NodeName: n.Name, Level: 3, State: StateRunning,
		Action: "new_instance", ActionState: ActionRunning,
		LevelStartedAt: start, StartedAt: start, UpdatedAt: start}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	// The in-flight trace row (it would be written when a real action starts; filled in here to verify timeout close-out).
	if err := db.Create(&storage.RecoveryAction{RecoveryID: rec.ID, NodeID: n.ID,
		NodeName: n.Name, Level: 3, Action: "new_instance", State: ActionRunning,
		StartedAt: start, UpdatedAt: start}).Error; err != nil {
		t.Fatal(err)
	}

	if err := Sweep(db, now, Options{}, Registry{3: &fakeAction{name: "new_instance"}}, nil); err != nil {
		t.Fatal(err)
	}

	// The trace row closes out as timeout with a reason.
	var trace storage.RecoveryAction
	if err := db.Where("recovery_id = ?", rec.ID).First(&trace).Error; err != nil {
		t.Fatal(err)
	}
	if trace.State != ActionTimeout || trace.Detail != "超时未恢复" || trace.FinishedAt == nil {
		t.Fatalf("trace = %+v", trace)
	}

	// The escalation alert is sent synchronously (Send runs inline in Sweep), exactly once;
	// the event outbox lands recovery_failed at the same time (HERALD-3).
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Event != "recovery_failed" || !strings.Contains(events[0].Text, n.Name) {
		t.Fatalf("event = %+v", events[0])
	}
	var ev storage.Event
	if err := db.Where("kind = ?", "recovery_failed").First(&ev).Error; err != nil {
		t.Fatalf("recovery_failed event missing: %v", err)
	}
	if ev.Severity != "critical" || ev.DedupKey != fmt.Sprintf("recovery:%d:recovery_failed", rec.ID) {
		t.Fatalf("event = %+v", ev)
	}
}

// TestLoopSweepsAndStops covers the periodic loop: the first round runs immediately
// (interval<=0 falls back to 60s) and the verdict node opens; Loop returns after ctx cancel.
func TestLoopSweepsAndStops(t *testing.T) {
	db := newTestDB(t)
	suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", time.Now().Add(-15*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // stop the Loop before closing the DB (t.Cleanup runs after the function returns)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(ctx, db, 0, Options{}, Registry{}, nil) // interval 0: takes the fallback branch
	}()

	// Only the first Sweep writes the recovery row; keep waiting even when the row is missing.
	var rec storage.Recovery
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := db.Where("node_id = ?", 1).First(&rec).Error
		if err == nil && rec.State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery not opened: %+v (err=%v)", rec, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.Level != 1 {
		t.Fatalf("opened = %+v", rec)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
}

// TestLastLine takes the tail log line as the error summary: blank lines trimmed before the last line; an empty string is safe.
func TestLastLine(t *testing.T) {
	cases := map[string]string{
		"a\nb\nc":   "c",
		"only":      "only",
		"x\n  \n":   "x",
		"":          "",
		"\n\nz\n\n": "z",
	}
	for in, want := range cases {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestActionNames pins the trace names of the three actions (Recovery.Action is stored by name).
func TestActionNames(t *testing.T) {
	if got := (&InstanceAction{}).Name(); got != "new_instance" {
		t.Errorf("instance name = %q", got)
	}
	if got := (&DNSAction{}).Name(); got != "dns_switch" {
		t.Errorf("dns name = %q", got)
	}
	if got := (&StandbyAction{}).Name(); got != "standby_promote" {
		t.Errorf("standby name = %q", got)
	}
}

// logBuf is a concurrency-safe log buffer (the Loop goroutine and the assertions read and write it together).
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// closeDB closes the underlying connection (the error-branch injection slot: an unavailable DB makes every layer surface errors or log them).
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestNodeByIDBranches covers the three node-lookup branches: found, missing returns
// (nil, nil), and a broken DB surfaces the error.
func TestNodeByIDBranches(t *testing.T) {
	db := newTestDB(t)
	n := suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", time.Now())
	got, err := nodeByID(db, n.ID)
	if err != nil || got == nil || got.Name != "entry-1" {
		t.Fatalf("found = %+v err=%v", got, err)
	}
	got, err = nodeByID(db, 999)
	if got != nil || err != nil {
		t.Fatalf("missing = %+v err=%v; want nil,nil", got, err)
	}
	closeDB(t, db)
	if got, err := nodeByID(db, n.ID); err == nil || got != nil {
		t.Fatalf("closed db = %+v err=%v; want error", got, err)
	}
}

// TestSweepNodeDeletedFails: the state row exists but the node was deleted - go
// straight to a terminal failure without hanging. Also covers the reg/logger defaults (nil registry, default logger).
func TestSweepNodeDeletedFails(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	rec := storage.Recovery{NodeID: 999, NodeName: "gone", Level: 1, State: StateRunning,
		ActionState: ActionUnset, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := loadRecovery(t, db, 999)
	if got.State != StateFailed || got.LastErr != "节点已删除" {
		t.Fatalf("deleted-node rec = %+v", got)
	}
}

// TestHeraldEmitFailureTolerated: event outbox failure - both verdict opening and the
// failed terminal state only log instead of blocking the state machine (an emit failure does not roll back the pipeline).
func TestHeraldEmitFailureTolerated(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now.Add(-15*time.Minute))
	if err := db.Migrator().DropTable(&storage.Event{}); err != nil {
		t.Fatal(err)
	}
	// Verdict opening: a failed event write still opens the pipeline.
	if err := Sweep(db, now, Options{}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	if rec := loadRecovery(t, db, 1); rec.State != StateRunning {
		t.Fatalf("open-line blocked by emit failure: %+v", rec)
	}
	// Failed terminal state: a failed escalation write still closes out.
	rec := storage.Recovery{NodeID: 999, NodeName: "gone", Level: 1, State: StateRunning,
		ActionState: ActionUnset, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := Sweep(db, now, Options{}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := loadRecovery(t, db, 999); got.State != StateFailed {
		t.Fatalf("finish blocked by emit failure: %+v", got)
	}
}

// TestSweepAndLoopOnClosedDB: DB unavailable - Sweep surfaces the error, and Loop only
// logs a Sweep failure without stopping (the next round keeps trying).
func TestSweepAndLoopOnClosedDB(t *testing.T) {
	db := newTestDB(t)
	closeDB(t, db)
	var buf logBuf
	lg := log.New(&buf, "", 0)
	if err := Sweep(db, time.Now(), Options{}, Registry{}, lg); err == nil {
		t.Fatal("sweep on closed db must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(ctx, db, 5*time.Millisecond, Options{}, Registry{}, lg)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not stop after cancel")
	}
	if !strings.Contains(buf.String(), "recovery sweep:") {
		t.Fatalf("loop log = %q; want sweep error logged", buf.String())
	}
}

// TestAdvanceOKAndRunningWindows covers the wait-window rules: an OK action within the
// timeout waits in place for probe recovery; an OK action past the timeout advances; a Running action is not duplicated within its window.
func TestAdvanceOKAndRunningWindows(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	n1 := suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now)
	n2 := suspendedNode(t, db, "entry-2", "连续 3 次探测 sick", now)
	lg := log.New(io.Discard, "", 0)
	opts := Options{LevelTimeout: time.Minute}

	okRec := storage.Recovery{NodeID: n1.ID, NodeName: "entry-1", Level: 1, State: StateRunning,
		Action: "dns_switch", ActionState: ActionOK, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&okRec).Error; err != nil {
		t.Fatal(err)
	}
	runRec := storage.Recovery{NodeID: n2.ID, NodeName: "entry-2", Level: 2, State: StateRunning,
		Action: "fake", ActionState: ActionRunning, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&runRec).Error; err != nil {
		t.Fatal(err)
	}

	// OK within the timeout: wait in place, do not advance.
	if err := advance(db, okRec, n1, now, opts, Registry{}, lg); err != nil {
		t.Fatal(err)
	}
	if got := loadRecovery(t, db, n1.ID); got.Level != 1 || got.ActionState != ActionOK {
		t.Fatalf("OK within window advanced: %+v", got)
	}
	// OK past the timeout: advance and reset the action state.
	if err := advance(db, okRec, n1, now.Add(2*time.Minute), opts, Registry{}, lg); err != nil {
		t.Fatal(err)
	}
	if got := loadRecovery(t, db, n1.ID); got.Level != 2 || got.ActionState != ActionUnset {
		t.Fatalf("OK timeout did not advance: %+v", got)
	}
	// Running within the window: no timeout, no movement.
	if err := advance(db, runRec, n2, now, opts, Registry{}, lg); err != nil {
		t.Fatal(err)
	}
	if got := loadRecovery(t, db, n2.ID); got.ActionState != ActionRunning || got.LastErr != "" {
		t.Fatalf("running within window disturbed: %+v", got)
	}
}

// TestRunActionTruncatesAndLateDrop covers writing back action results: an over-long
// error keeps only the last 500 characters; late results are dropped once the pipeline is terminal.
func TestRunActionTruncatesAndLateDrop(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	node := suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now)
	rec := storage.Recovery{NodeID: node.ID, NodeName: "entry-1", Level: 1, State: StateRunning,
		Action: "fake", ActionState: ActionRunning, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	lg := log.New(io.Discard, "", 0)

	// Over-long error: last_err is truncated to the last 500 characters (keeping the tail scene).
	long := errors.New(strings.Repeat("x", 600))
	runAction(context.Background(), db, &fakeAction{name: "fake", err: long}, rec, node, lg)
	got := loadRecovery(t, db, node.ID)
	if got.ActionState != ActionFailed || len(got.LastErr) != 500 {
		t.Fatalf("truncated = state %s errLen %d", got.ActionState, len(got.LastErr))
	}

	// Late result: the pipeline is already terminal, so the write back misses and is dropped.
	if err := db.Model(&storage.Recovery{}).Where("id = ?", got.ID).
		Update("state", StateDone).Error; err != nil {
		t.Fatal(err)
	}
	runAction(context.Background(), db, &fakeAction{name: "fake", err: errors.New("late")}, got, node, lg)
	if got := loadRecovery(t, db, node.ID); got.State != StateDone {
		t.Fatalf("late result overwrote terminal state: %+v", got)
	}
}

// TestTraceHelpersOnClosedDB covers trace-helper tolerance: with the DB unavailable, traces
// only log without panicking, while finish/advance-class helpers surface errors to the caller.
func TestTraceHelpersOnClosedDB(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	node := suspendedNode(t, db, "entry-1", "连续 3 次探测 sick", now)
	rec := storage.Recovery{NodeID: node.ID, NodeName: "entry-1", Level: 1, State: StateRunning,
		Action: "fake", ActionState: ActionUnset, LevelStartedAt: now, StartedAt: now, UpdatedAt: now}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	var buf logBuf
	lg := log.New(&buf, "", 0)
	closeDB(t, db)

	traceRecovery(db, rec, storage.RecoveryAction{RecoveryID: rec.ID, Level: 1,
		State: ActionRunning, StartedAt: now, UpdatedAt: now}, lg)
	finishTrace(db, rec, ActionFailed, "boom", now, lg)
	if err := finishRecovery(db, rec, StateFailed, "库没了"); err == nil {
		t.Fatal("finishRecovery on closed db must fail")
	}
	if err := advanceNext(db, rec, now, lg); err == nil {
		t.Fatal("advanceNext on closed db must fail")
	}
	// Unregistered level: the trace update write fails and surfaces the error (before the skipped advancement).
	if err := advance(db, rec, node, now, Options{}, Registry{}, lg); err == nil {
		t.Fatal("advance unregistered on closed db must fail")
	}
	// Registered level: the update setting running fails and surfaces the error.
	if err := advance(db, rec, node, now, Options{}, Registry{1: &fakeAction{name: "fake"}}, lg); err == nil {
		t.Fatal("advance registered on closed db must fail")
	}
	if !strings.Contains(buf.String(), "recovery trace:") ||
		!strings.Contains(buf.String(), "recovery trace finish:") {
		t.Fatalf("trace logs = %q", buf.String())
	}
}

// TestCooldownGuardsVerdict covers F-1644: after a pipeline reaches a terminal
// state (failed here), the still-suspended node sits out the cooldown instead
// of re-entering a fresh pipeline on the next sweep; once the cooldown passes,
// the verdict opens again.
func TestCooldownGuardsVerdict(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	cool := 30 * time.Minute
	suspendedNode(t, db, "entry-cd", "连续 3 次探测 sick", now.Add(-15*time.Minute))

	// Round 1: the pipeline opens, then terminates as failed (the node stays suspended).
	if err := Sweep(db, now, Options{Cooldown: cool}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	rec := loadRecovery(t, db, 1)
	if err := db.Model(&storage.Recovery{}).Where("id = ?", rec.ID).
		Updates(map[string]any{"state": StateFailed, "finished_at": now, "updated_at": now, "last_err": "L3 timeout"}).Error; err != nil {
		t.Fatal(err)
	}

	// Round 2 inside the cooldown: no fresh verdict for the suspended node.
	if err := Sweep(db, now.Add(5*time.Minute), Options{Cooldown: cool}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.Recovery{}).Count(&n)
	if n != 1 {
		t.Fatalf("cooldown should bar a new pipeline, rows = %d", n)
	}

	// Round 3 past the cooldown: the same row (one per node, uniqueIndex) is
	// reset into a fresh L1 run.
	if err := Sweep(db, now.Add(cool+time.Minute), Options{Cooldown: cool}, Registry{}, nil); err != nil {
		t.Fatal(err)
	}
	var rows []storage.Recovery
	db.Order("id").Find(&rows)
	if len(rows) != 1 || rows[0].State != StateRunning || rows[0].Level != 1 || rows[0].FinishedAt != nil {
		t.Fatalf("post-cooldown verdict = %+v", rows)
	}
}
