//go:build unix

// Package procs manages the local proxy processes: start/stop per spec, automatic crash restart (with backoff), reload and status reporting.
package procs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// reload policy values.
const (
	ReloadRestart = "restart"
	ReloadSignal  = "signal"
)

// defaultMetricsInterval is the default interval for collecting process metrics (SAVE-3:
// the sampling tier at which metrics like hit statistics are reported with the heartbeat).
const defaultMetricsInterval = 15 * time.Second

// Manager manages every managed process.
type Manager struct {
	log *log.Logger

	mu    sync.Mutex
	procs map[string]*proc

	// OnStatusChange is called asynchronously when a process changes state, for reporting proc_report.
	OnStatusChange func(status agentproto.ProcStatus)
	// OnAlarm is called asynchronously on crashes and other anomalies, for reporting an alarm.
	OnAlarm func(alarm agentproto.Alarm)

	minBackoff time.Duration
	maxBackoff time.Duration
	stableRun  time.Duration // reset the backoff after running continuously for longer than this

	// metricsInterval is the interval for collecting process metrics (MetricsURL).
	metricsInterval time.Duration
}

// proc holds the runtime state and supervision inputs of one managed process.
type proc struct {
	mgr  *Manager
	spec config.ProcSpec

	mu       sync.Mutex
	state    string
	since    time.Time
	restarts int
	desired  bool // expected to run (true = running)
	stopping bool // the exit was triggered by a stop/reload command (as opposed to a crash)
	pid      int
	logs     *logRing // runtime log ring buffer (P1-11), the stdout/stderr mirror

	req    chan ctrlReq
	exited chan struct{} // closed when the current process exits; a closed channel on startup failure
	kill   func()        // terminate the current process

	// metrics: snapshot of the last successful collection
	metrics map[string]uint64
}

type ctrlReq struct {
	action string
	reply  chan error
}

// New creates a manager from the spec table (no process is started; Start supervises them all together).
func New(specs []config.ProcSpec, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	m := &Manager{
		log:             logger,
		procs:           map[string]*proc{},
		metricsInterval: defaultMetricsInterval,
		minBackoff:      time.Second,
		maxBackoff:      30 * time.Second,
		stableRun:       time.Minute,
	}
	for _, spec := range specs {
		m.procs[spec.Name] = &proc{
			mgr:    m,
			spec:   spec,
			state:  agentproto.ProcStopped,
			logs:   newLogRing(procLogCap),
			req:    make(chan ctrlReq, 8),
			exited: closedChan(),
			kill:   func() {},
		}
	}
	return m
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// Start raises a supervision loop for every process; all processes start themselves when the agent starts.
func (m *Manager) Start(ctx context.Context) {
	for _, p := range m.procs {
		p.mu.Lock()
		p.desired = true
		p.mu.Unlock()
		// Binary-missing precheck (hy2 batch): probe the exec file explicitly before starting,
		// so a missing one raises an alarm right away (the panel sees a clear reason) instead of waiting for the first launch failure --
		// the supervision loop keeps retrying with backoff and the process comes up once the binary is installed.
		if _, err := os.Stat(p.spec.Exec); err != nil {
			m.alarm(p, agentproto.AlarmKindProcCrash, agentproto.AlarmSeverityCritical,
				fmt.Sprintf("binary not found: %s (fix procs[].exec or install the engine)", p.spec.Exec))
		}
		go m.supervise(ctx, p)
		// SAVE-3: a process with a metrics endpoint (MetricsURL) also gets a collection goroutine,
		// whose result is attached to the status snapshot and reported with the heartbeat.
		if p.spec.MetricsURL != "" {
			go m.collectProcMetrics(ctx, p)
		}
	}
}

// Statuses returns snapshots of every process state (for the heartbeat).
func (m *Manager) Statuses() []agentproto.ProcStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.procs))
	for name := range m.procs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]agentproto.ProcStatus, 0, len(names))
	for _, n := range names {
		out = append(out, m.procs[n].status())
	}
	return out
}

// Control executes a panel command: start/stop/reload, blocking until it lands or times out.
func (m *Manager) Control(name, action string) error {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil {
		return fmt.Errorf("unknown proc %q", name)
	}
	switch action {
	case agentproto.ProcActionStart, agentproto.ProcActionStop, agentproto.ProcActionReload:
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	reply := make(chan error, 1)
	select {
	case p.req <- ctrlReq{action: action, reply: reply}:
	case <-time.After(5 * time.Second):
		return errors.New("proc busy")
	}
	select {
	case err := <-reply:
		return err
	case <-time.After(15 * time.Second):
		return errors.New("control timeout")
	}
}

// Logs returns the most recent limit lines of a process's runtime log (P1-11); an unknown process errors.
func (m *Manager) Logs(name string, limit int) ([]string, error) {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown proc %q", name)
	}
	return p.logs.snapshot(limit), nil
}

// supervise is the supervision loop for one process: keep it alive while expected to run, restart crashes with backoff, and answer commands immediately.
func (m *Manager) supervise(ctx context.Context, p *proc) {
	backoff := m.minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		if !p.desiredRun() {
			p.clearStopping()
			p.setState(agentproto.ProcStopped)
			select {
			case <-ctx.Done():
				return
			case r := <-p.req:
				backoff = m.applyWhenStopped(p, r)
			}
			continue
		}

		startedAt := time.Now()
		if err := m.launch(p); err != nil {
			// a failed start is treated as a crash: retry after backoff; the alarm carries the exec path and the underlying
			// error (missing binary/permissions etc. give the panel a clear reason, never silent).
			p.setState(agentproto.ProcCrashed)
			m.alarm(p, agentproto.AlarmKindProcCrash, agentproto.AlarmSeverityCritical,
				fmt.Sprintf("launch failed: exec %s: %v", p.spec.Exec, err))
			backoff = m.backoffWait(ctx, p, backoff)
			continue
		}

		select {
		case <-ctx.Done():
			p.killCurrent()
			return
		case r := <-p.req:
			m.handleRunningReq(p, r)
		case <-p.exited:
			ranFor := time.Since(startedAt)
			if p.consumeStopping() {
				// an exit triggered by stop/reload: back to the top of the loop, where desired decides whether to start again.
				continue
			}
			// an unexpected exit: crash alarm and a backoff restart.
			p.setState(agentproto.ProcCrashed)
			p.bumpRestarts()
			m.alarm(p, agentproto.AlarmKindProcCrash, agentproto.AlarmSeverityWarning,
				fmt.Sprintf("proc exited unexpectedly (restarts=%d)", p.restartCount()))
			if ranFor >= m.stableRun {
				backoff = m.minBackoff
			}
			m.log.Printf("proc %s crashed, restart in %s", p.spec.Name, backoff)
			backoff = m.backoffWait(ctx, p, backoff)
		}
	}
}

// backoffWait waits out a backoff while still answering commands; it returns the next backoff segment.
func (m *Manager) backoffWait(ctx context.Context, p *proc, backoff time.Duration) time.Duration {
	select {
	case <-ctx.Done():
		return backoff
	case r := <-p.req:
		return m.applyWhenStopped(p, r)
	case <-time.After(backoff):
	}
	next := backoff * 2
	if next > m.maxBackoff {
		next = m.maxBackoff
	}
	return next
}

// launch starts the process; p.exited/p.kill are guaranteed usable whether it succeeds or not, and the start error is returned
// (nil = started; the process exiting is learned separately through the exited channel).
func (m *Manager) launch(p *proc) error {
	cmd := exec.Command(p.spec.Exec, p.spec.Args...)
	cmd.Dir = p.spec.WorkDir
	// runtime log mirror: written to the process ring buffer in addition to the agent log, for the panel to fetch (P1-11).
	cmd.Stdout = io.MultiWriter(m.log.Writer(), p.logs)
	cmd.Stderr = io.MultiWriter(m.log.Writer(), p.logs)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// after a child exits, a grandchild may still hold the stdio pipe write end (nginx workers, daemonizing kernels
	// all do this), and Wait would block until the pipe closes, hanging the supervision loop and stalling
	// crash detection. A 1s deadline forces it closed: normal exits that close the pipe promptly are unaffected.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		m.log.Printf("proc %s launch: %v", p.spec.Name, err)
		p.mu.Lock()
		p.pid = 0
		p.kill = func() {}
		p.exited = closedChan()
		p.mu.Unlock()
		return err
	}
	exited := make(chan struct{})
	pgid := -cmd.Process.Pid
	p.mu.Lock()
	p.pid = cmd.Process.Pid
	p.kill = func() { _ = syscall.Kill(pgid, syscall.SIGTERM) }
	p.exited = exited
	p.mu.Unlock()
	p.setState(agentproto.ProcRunning)

	go func() {
		err := cmd.Wait()
		if err != nil {
			m.log.Printf("proc %s exited: %v", p.spec.Name, err)
		}
		close(exited)
	}()
	return nil
}

// applyWhenStopped handles commands while the process is stopped and returns the starting point of the following backoff.
func (m *Manager) applyWhenStopped(p *proc, r ctrlReq) time.Duration {
	defer close(r.reply)
	switch r.action {
	case agentproto.ProcActionStart, agentproto.ProcActionReload:
		// while stopped, start and reload are equivalent: expect it to run again.
		p.mu.Lock()
		p.desired = true
		p.mu.Unlock()
		r.reply <- nil
	case agentproto.ProcActionStop:
		// stop can arrive both while stopped and while crash-backing-off: in both cases expect it not to run.
		p.mu.Lock()
		p.desired = false
		p.mu.Unlock()
		r.reply <- nil
	default:
		r.reply <- fmt.Errorf("unknown action %q", r.action)
	}
	return m.minBackoff
}

// handleRunningReq handles commands while the process is running.
func (m *Manager) handleRunningReq(p *proc, r ctrlReq) {
	switch r.action {
	case agentproto.ProcActionStop:
		p.mu.Lock()
		p.desired = false
		p.stopping = true
		kill := p.kill
		p.mu.Unlock()
		kill()
		<-p.exited
		r.reply <- nil
	case agentproto.ProcActionReload:
		r.reply <- m.reload(p)
	case agentproto.ProcActionStart:
		r.reply <- errors.New("already running")
	}
}

// reload follows the reload policy: signal sends SIGHUP, restart stops the process and the supervision loop brings it back.
func (m *Manager) reload(p *proc) error {
	if p.spec.Reload == ReloadSignal {
		p.mu.Lock()
		pid := p.pid
		p.mu.Unlock()
		if pid == 0 {
			return errors.New("not running")
		}
		if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
			return err
		}
		return nil
	}
	p.mu.Lock()
	p.stopping = true
	kill := p.kill
	p.mu.Unlock()
	kill()
	<-p.exited // the supervision loop then starts it immediately with desired=true
	return nil
}

func (p *proc) desiredRun() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.desired
}

func (p *proc) clearStopping() {
	p.mu.Lock()
	p.stopping = false
	p.mu.Unlock()
}

// consumeStopping returns and clears the stopping flag: whether the exit was triggered by a command.
func (p *proc) consumeStopping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stopping
	p.stopping = false
	return s
}

func (p *proc) killCurrent() {
	p.mu.Lock()
	kill := p.kill
	p.mu.Unlock()
	kill()
}

func (p *proc) bumpRestarts() {
	p.mu.Lock()
	p.restarts++
	p.mu.Unlock()
}

func (p *proc) restartCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.restarts
}

func (p *proc) setState(state string) {
	p.mu.Lock()
	p.state = state
	p.since = time.Now()
	status := p.snapshotLocked()
	p.mu.Unlock()
	if p.mgr.OnStatusChange != nil {
		go p.mgr.OnStatusChange(status)
	}
}

func (p *proc) snapshotLocked() agentproto.ProcStatus {
	return agentproto.ProcStatus{
		Name:     p.spec.Name,
		State:    p.state,
		Since:    p.since,
		Restarts: p.restarts,
		PID:      p.pid,
		Metrics:  p.metrics,
	}
}

func (p *proc) status() agentproto.ProcStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (m *Manager) alarm(p *proc, kind, severity, message string) {
	if m.OnAlarm == nil {
		return
	}
	go m.OnAlarm(agentproto.Alarm{
		Kind: kind, Severity: severity, Proc: p.spec.Name, Message: message, At: time.Now(),
	})
}

// collectProcMetrics keeps polling one process's MetricsURL and caches the result;
// a failed collection (unreachable/non-200/parse error) clears the snapshot, and a cancelled ctx exits.
func (m *Manager) collectProcMetrics(ctx context.Context, p *proc) {
	parser := newMetricsParser()
	client := &http.Client{Timeout: 10 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.metricsInterval):
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.spec.MetricsURL, nil)
		if err != nil {
			p.setMetrics(nil)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			p.setMetrics(nil)
			continue
		}
		var metrics map[string]uint64
		if resp.StatusCode == http.StatusOK {
			if parsed, err := parser.Parse(resp.Body); err == nil {
				metrics = parsed
			}
		}
		resp.Body.Close()
		p.setMetrics(metrics)
	}
}

// setMetrics updates the process metrics snapshot (under p.mu, matching the status() read side).
func (p *proc) setMetrics(metrics map[string]uint64) {
	p.mu.Lock()
	p.metrics = metrics
	p.mu.Unlock()
}
