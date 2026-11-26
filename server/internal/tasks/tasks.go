// Package tasks is the unified background-task runner (F-1602~F-1615): one
// ledger table (storage.Task), one shared state machine, and one sweep loop
// that claim-runs due tasks with per-attempt timeout, bounded retry, exponential
// backoff, cancel, idempotent enqueue, progress/stage updates, a structured log
// trail, a concurrency quota and backlog/dead-letter alerting. Task-shaped work
// registers a Definition (kind) here instead of growing another bespoke table;
// the five legacy task tables keep their rows and migrate over time.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Task states (F-1602): the shared vocabulary. pending rows with a due
// next_attempt_at are the queue (F-1603); dead means the retry budget ran out.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusDead      = "dead"
)

// Alert kinds reuse the Herald vocabulary (F-1615).
const (
	// AlertBacklog fires when pending tasks stay older than the backlog window.
	AlertBacklog = "task_backlog"
	// AlertDead fires when dead-letter rows accumulate.
	AlertDead = "task_dead"
)

// Defaults for definitions that leave a field at its zero value.
const (
	DefaultMaxAttempts  = 3
	DefaultBaseDelay    = 5 * time.Second
	DefaultMaxDelay     = time.Hour
	DefaultTimeout      = 5 * time.Minute
	DefaultSweepSeconds = 2
	DefaultConcurrency  = 4

	// Alert thresholds and windows (F-1615), overridable via Settings.
	settingBacklogThreshold = "task.backlog_threshold" // pending rows older than the window
	settingDeadThreshold    = "task.dead_threshold"    // dead rows fleet-wide
	settingConcurrency      = "task.concurrency"       // worker quota (F-1614)
	defaultBacklogThreshold = 50
	defaultDeadThreshold    = 10
	backlogWindow           = 10 * time.Minute
	alertEvery              = time.Hour
)

// ErrUnknownKind is returned when enqueueing an unregistered kind.
var ErrUnknownKind = errors.New("unknown task kind")

// Definition registers one runnable kind with its retry/timeout policy.
type Definition struct {
	// MaxAttempts is the retry budget including the first try (<=0 -> default 3, F-1605).
	MaxAttempts int
	// BaseDelay/MaxDelay shape the exponential backoff base*2^(attempt-1) (F-1606).
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// Timeout bounds each attempt (<=0 -> default 5m; the per-attempt ctx deadline, F-1604).
	Timeout time.Duration
	// Depends is the pre-execution dependency check (F-1630): a non-nil error
	// fails the attempt with stage=depends before Run is called.
	Depends func(ctx context.Context, t storage.Task) error
	// Run executes one attempt. It may call (*Manager).SetProgress to publish
	// stage/progress (F-1609) and may write a Result summary (typically JSON;
	// per-item partial success is a succeeded task whose result carries per-item
	// ok/error entries, F-1612).
	Run func(ctx context.Context, t *storage.Task) error
}

// Manager owns the sweep loop and the registered kinds.
type Manager struct {
	db       *gorm.DB
	logf     func(format string, args ...any)
	executor string

	mu      sync.Mutex
	defs    map[string]Definition
	quota   int
	alertAt map[string]time.Time

	cancelMu sync.Mutex
	cancels  map[uint]context.CancelFunc
	semCh    chan struct{}
	// asked records explicit cancel requests for running rows; a bare timeout
	// (DeadlineExceeded without a request) stays a retryable failure instead.
	asked map[uint]time.Time
}

// NewManager builds a manager over the store; logf may be nil.
func NewManager(db *gorm.DB, logf func(format string, args ...any)) *Manager {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	exec, err := os.Hostname()
	if err != nil || exec == "" {
		exec = "server"
	}
	return &Manager{
		db:       db,
		logf:     logf,
		executor: exec,
		defs:     map[string]Definition{},
		quota:    DefaultConcurrency,
		alertAt:  map[string]time.Time{},
		cancels:  map[uint]context.CancelFunc{},
		asked:    map[uint]time.Time{},
	}
}

// Register makes a kind runnable; re-registering replaces the definition.
func (m *Manager) Register(kind string, def Definition) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defs[kind] = def
}

// Registered reports whether the kind can be enqueued.
func (m *Manager) Registered(kind string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.defs[kind]
	return ok
}

// Kinds lists the registered kinds (for the enqueue surface's validation hint).
func (m *Manager) Kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.defs))
	for k := range m.defs {
		out = append(out, k)
	}
	return out
}

func (m *Manager) def(kind string) (Definition, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.defs[kind]
	return d, ok
}

// Enqueue persists one task row (F-1603/F-1608): the row starts pending with a
// due next_attempt_at; with an idempotency key a re-enqueue returns the
// existing row untouched. Unknown kinds are rejected so the API surface cannot
// mint rows nothing will ever run.
func (m *Manager) Enqueue(kind string, refID uint, idempotencyKey, actor string) (storage.Task, error) {
	def, ok := m.def(kind)
	if !ok {
		return storage.Task{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	if idempotencyKey == "" {
		// The column is uniquely indexed; keys are optional, so synthesized keys
		// keep keyless enqueues collision-free.
		idempotencyKey = fmt.Sprintf("auto:%s:%d", kind, time.Now().UnixNano())
	}
	max := def.MaxAttempts
	if max <= 0 {
		max = DefaultMaxAttempts
	}
	// The column carries whole seconds; a sub-second definition timeout rounds
	// up to 1s instead of truncating to 0 (which would silently fall back to
	// the 300s default and neuter the attempt deadline).
	timeout := int((def.Timeout + time.Second - 1) / time.Second)
	if def.Timeout > 0 && timeout < 1 {
		timeout = 1
	}
	if timeout <= 0 {
		timeout = int(DefaultTimeout / time.Second)
	}
	now := time.Now()
	row := storage.Task{
		Kind:           kind,
		RefID:          refID,
		Status:         StatusPending,
		Stage:          "queued",
		MaxAttempts:    max,
		TimeoutSeconds: timeout,
		NextAttemptAt:  &now,
		IdempotencyKey: idempotencyKey,
		Actor:          actor,
	}
	err := m.db.Create(&row).Error
	if err != nil && isUniqueViolation(err) {
		// F-1608: same key -> return the existing row, enqueue is idempotent.
		var existing storage.Task
		if lookup := m.db.Where("idempotency_key = ?", idempotencyKey).First(&existing).Error; lookup == nil {
			return existing, nil
		}
		return existing, err
	}
	if err != nil {
		return storage.Task{}, err
	}
	m.appendLog(&row, "enqueued by "+actorString(actor))
	return row, nil
}

// SetProgress publishes stage/progress from inside Run (F-1609): best-effort,
// only while the row is running.
func (m *Manager) SetProgress(taskID uint, stage string, progress int) {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	m.db.Model(&storage.Task{}).
		Where("id = ? AND status = ?", taskID, StatusRunning).
		Updates(map[string]any{"stage": stage, "progress": progress})
}

func (m *Manager) appendLog(t *storage.Task, line string) {
	entry := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), line)
	m.db.Model(&storage.Task{}).Where("id = ?", t.ID).
		Update("log_lines", gorm.Expr("COALESCE(log_lines, '') || ?", entry))
}

// Loop sweeps the queue until the context ends (F-1603): due pending rows are
// claimed up to the concurrency quota (F-1614) and run in the background.
func (m *Manager) Loop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSweepSeconds * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep(ctx)
			m.maybeAlert()
		}
	}
}

// LoopOnce drives one sweep + alert pass (tests and manual one-shots).
func (m *Manager) LoopOnce(ctx context.Context) {
	m.sweep(ctx)
	m.maybeAlert()
}

// sweep claims and launches due tasks bounded by the quota.
func (m *Manager) sweep(ctx context.Context) {
	m.reloadQuota()
	for {
		if !m.tryAcquire() {
			return
		}
		row, ok := m.claimOne()
		if !ok {
			m.release()
			return
		}
		go func(t storage.Task) {
			defer m.release()
			m.runOne(ctx, t)
		}(row)
	}
}

// claimOne flips one due pending row to running (row-level claim via the
// status update guarding against double claim).
func (m *Manager) claimOne() (storage.Task, bool) {
	now := time.Now()
	var candidate storage.Task
	err := m.db.Where("status = ? AND next_attempt_at <= ?", StatusPending, now).
		Order("id ASC").First(&candidate).Error
	if err != nil {
		return storage.Task{}, false
	}
	res := m.db.Model(&storage.Task{}).
		Where("id = ? AND status = ?", candidate.ID, StatusPending).
		Updates(map[string]any{
			"status":          StatusRunning,
			"stage":           "run",
			"attempts":        gorm.Expr("attempts + 1"),
			"executor":        m.executor,
			"next_attempt_at": nil,
		})
	if res.Error != nil || res.RowsAffected == 0 {
		return storage.Task{}, false
	}
	candidate.Status = StatusRunning
	candidate.Attempts++
	candidate.Executor = m.executor
	return candidate, true
}

// runOne executes one attempt under the definition's policy (F-1604/F-1630):
// depends precheck, timeout ctx, then success / cancel / retry-or-dead.
func (m *Manager) runOne(parent context.Context, t storage.Task) {
	def, ok := m.def(t.Kind)
	if !ok {
		// Definition vanished (e.g. code roll): park the row instead of looping it forever.
		m.finishFailure(t, "definition missing", "orphaned")
		return
	}
	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	m.cancelMu.Lock()
	m.cancels[t.ID] = cancel
	m.cancelMu.Unlock()
	// A cancel that arrived between the claim and this registration (the row
	// was already running from Cancel's view, but no ctx existed yet) must not
	// fall through to Run: honour it here, before any work happens.
	if m.cancelAsked(t.ID) {
		m.db.Model(&storage.Task{}).Where("id = ?", t.ID).Updates(map[string]any{
			"status": StatusCancelled,
			"stage":  "cancelled",
			"error":  "cancelled",
		})
		m.appendLog(&t, fmt.Sprintf("attempt=%d cancelled before start", t.Attempts))
		return
	}
	defer func() {
		cancel()
		m.cancelMu.Lock()
		delete(m.cancels, t.ID)
		delete(m.asked, t.ID)
		m.cancelMu.Unlock()
	}()

	mut := t // Run may mutate Result/Stage via the manager
	if def.Depends != nil {
		if err := def.Depends(ctx, t); err != nil {
			m.attemptFailed(&mut, def, "depends", err)
			return
		}
	}
	runErr := def.Run(ctx, &mut)
	switch {
	case runErr == nil:
		m.db.Model(&storage.Task{}).Where("id = ?", t.ID).Updates(map[string]any{
			"status":   StatusSucceeded,
			"stage":    "done",
			"progress": 100,
			"result":   mut.Result,
			"error":    "",
		})
		m.appendLog(&mut, fmt.Sprintf("attempt=%d ok", t.Attempts))
	case errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded):
		// A cancel request wins over a deadline: the row only becomes cancelled
		// when the row was asked to cancel; a plain timeout is a failed attempt
		// (retryable like any other).
		if m.cancelAsked(t.ID) {
			m.db.Model(&storage.Task{}).Where("id = ?", t.ID).Updates(map[string]any{
				"status": StatusCancelled,
				"stage":  "cancelled",
				"error":  "cancelled",
			})
			m.appendLog(&mut, fmt.Sprintf("attempt=%d cancelled", t.Attempts))
			return
		}
		m.attemptFailed(&mut, def, "timeout", errors.New("attempt timed out"))
	default:
		m.attemptFailed(&mut, def, "run", runErr)
	}
}

// attemptFailed applies the retry budget: retryable rows go back to pending
// with backoff, exhausted rows die (F-1605/F-1606).
func (m *Manager) attemptFailed(t *storage.Task, def Definition, stage string, runErr error) {
	errText := runErr.Error()
	if len(errText) > 512 {
		errText = errText[:512]
	}
	if t.Attempts >= t.MaxAttempts {
		m.finishFailure(*t, errText, stage)
		return
	}
	base := def.BaseDelay
	if base <= 0 {
		base = DefaultBaseDelay
	}
	max := def.MaxDelay
	if max <= 0 {
		max = DefaultMaxDelay
	}
	delay := base << (t.Attempts - 1) // attempts is already incremented: first retry uses base
	if delay > max || delay <= 0 {
		delay = max
	}
	next := time.Now().Add(delay)
	m.db.Model(&storage.Task{}).Where("id = ?", t.ID).Updates(map[string]any{
		"status":          StatusPending,
		"stage":           stage,
		"error":           errText,
		"next_attempt_at": &next,
	})
	m.appendLog(t, fmt.Sprintf("attempt=%d failed stage=%s err=%s; retry in %s", t.Attempts, stage, errText, delay))
}

// finishFailure marks the terminal failure (retry budget exhausted or orphaned).
func (m *Manager) finishFailure(t storage.Task, errText, stage string) {
	if len(errText) > 512 {
		errText = errText[:512]
	}
	m.db.Model(&storage.Task{}).Where("id = ?", t.ID).Updates(map[string]any{
		"status": StatusDead,
		"stage":  stage,
		"error":  errText,
	})
	m.appendLog(&t, fmt.Sprintf("attempt=%d dead stage=%s err=%s", t.Attempts, stage, errText))
}

// Cancel cancels a task (F-1607): pending rows flip to cancelled right away;
// running rows get their attempt ctx cancelled and the run maps that to
// cancelled. Finished rows report as not cancellable.
func (m *Manager) Cancel(id uint) (string, error) {
	var t storage.Task
	if err := m.db.First(&t, id).Error; err != nil {
		return "", err
	}
	switch t.Status {
	case StatusPending:
		m.db.Model(&storage.Task{}).Where("id = ?", id).Updates(map[string]any{
			"status": StatusCancelled,
			"stage":  "cancelled",
		})
		m.appendLog(&t, "cancelled while pending")
		return StatusCancelled, nil
	case StatusRunning:
		m.cancelMu.Lock()
		cancel, ok := m.cancels[id]
		m.asked[id] = time.Now()
		m.cancelMu.Unlock()
		if ok {
			cancel()
		}
		return StatusRunning, nil // cancellation lands when the run observes the ctx
	default:
		return t.Status, fmt.Errorf("task already %s", t.Status)
	}
}

// Retry re-queues a terminal task manually (F-1611): attempts reset so the
// full budget applies again; actor records who asked.
func (m *Manager) Retry(id uint, actor string) (storage.Task, error) {
	var t storage.Task
	if err := m.db.First(&t, id).Error; err != nil {
		return t, err
	}
	switch t.Status {
	case StatusSucceeded, StatusRunning, StatusPending:
		return t, fmt.Errorf("task is %s; only failed/dead/cancelled tasks can be retried", t.Status)
	}
	now := time.Now()
	if err := m.db.Model(&storage.Task{}).Where("id = ?", id).Updates(map[string]any{
		"status":          StatusPending,
		"stage":           "queued",
		"attempts":        0,
		"error":           "",
		"next_attempt_at": &now,
		"actor":           actor,
	}).Error; err != nil {
		return t, err
	}
	m.appendLog(&t, "manual retry by "+actorString(actor))
	return m.reloadRow(id), nil
}

func (m *Manager) reloadRow(id uint) storage.Task {
	var t storage.Task
	_ = m.db.First(&t, id).Error
	return t
}

// cancelAsked reports whether Cancel explicitly targeted this running row
// (as opposed to the attempt's own deadline firing).
func (m *Manager) cancelAsked(id uint) bool {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	_, ok := m.asked[id]
	return ok
}

// maybeAlert emits backlog/dead-letter alerts at most once per window (F-1615).
func (m *Manager) maybeAlert() {
	now := time.Now()
	staleBefore := now.Add(-backlogWindow)
	var backlog int64
	_ = m.db.Model(&storage.Task{}).Where("status = ? AND next_attempt_at < ?", StatusPending, staleBefore).Count(&backlog).Error
	var dead int64
	_ = m.db.Model(&storage.Task{}).Where("status = ?", StatusDead).Count(&dead).Error

	backlogThreshold := intThreshold(m.db, settingBacklogThreshold, defaultBacklogThreshold)
	deadThreshold := intThreshold(m.db, settingDeadThreshold, defaultDeadThreshold)

	m.mu.Lock()
	defer m.mu.Unlock()
	if backlog >= int64(backlogThreshold) && now.Add(-alertEvery).After(m.alertAt[AlertBacklog]) {
		m.alertAt[AlertBacklog] = now
		_, err := herald.Emit(m.db, herald.EmitInput{
			Kind:     herald.KindTaskBacklog,
			Severity: herald.SeverityWarning,
			Title:    "任务积压",
			Body:     fmt.Sprintf("超过 %s 未启动的任务积压 %d 条（阈值 %d）。", backlogWindow, backlog, backlogThreshold),
			DedupKey: "tasks:backlog",
		})
		m.logf("tasks: backlog alert emitted (%d pending) err=%v", backlog, err)
	}
	if dead >= int64(deadThreshold) && now.Add(-alertEvery).After(m.alertAt[AlertDead]) {
		m.alertAt[AlertDead] = now
		_, err := herald.Emit(m.db, herald.EmitInput{
			Kind:     herald.KindTaskDead,
			Severity: herald.SeverityWarning,
			Title:    "任务死信堆积",
			Body:     fmt.Sprintf("重试预算耗尽的死信任务累计 %d 条（阈值 %d），请到任务列表人工重试或排查。", dead, deadThreshold),
			DedupKey: "tasks:dead",
		})
		m.logf("tasks: dead alert emitted (%d dead) err=%v", dead, err)
	}
}

// reloadQuota re-reads the concurrency quota (F-1614) from Settings.
func (m *Manager) reloadQuota() {
	if n := intThreshold(m.db, settingConcurrency, DefaultConcurrency); n >= 1 && n != m.quota {
		m.mu.Lock()
		m.quota = n
		m.mu.Unlock()
	}
}

func (m *Manager) tryAcquire() bool {
	select {
	case m.sem() <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m *Manager) release() {
	<-m.sem()
}

// sem lazily sizes the channel to the current quota (resize = replace).
func (m *Manager) sem() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.semCh == nil || cap(m.semCh) != m.quota {
		m.semCh = make(chan struct{}, m.quota)
	}
	return m.semCh
}

func intThreshold(db *gorm.DB, key string, def int) int {
	v, ok, err := storage.GetSetting(db, key)
	if err != nil || !ok {
		return def
	}
	n := 0
	for _, c := range strings.TrimSpace(v) {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}

func isUniqueViolation(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicated key")
}

func actorString(actor string) string {
	if actor == "" {
		return "system"
	}
	return actor
}
