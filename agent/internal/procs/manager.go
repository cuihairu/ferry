//go:build unix

// Package procs 管理本机代理进程：按规格启停、崩溃自动拉起（退避）、reload 与状态上报。
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

// reload 策略取值。
const (
	ReloadRestart = "restart"
	ReloadSignal  = "signal"
)

// defaultMetricsInterval 是进程指标采集的缺省间隔（SAVE-3：
// 命中统计等指标随心跳上报的采样档）。
const defaultMetricsInterval = 15 * time.Second

// Manager 管理全部被管进程。
type Manager struct {
	log *log.Logger

	mu    sync.Mutex
	procs map[string]*proc

	// OnStatusChange 在进程状态变化时被异步调用，供上报 proc_report。
	OnStatusChange func(status agentproto.ProcStatus)
	// OnAlarm 在崩溃等异常时被异步调用，供上报 alarm。
	OnAlarm func(alarm agentproto.Alarm)

	minBackoff time.Duration
	maxBackoff time.Duration
	stableRun  time.Duration // 连续运行超过该时长后重置退避

	// metricsInterval 是进程指标（MetricsURL）采集间隔。
	metricsInterval time.Duration
}

// proc 是单个被管进程的运行态与监管输入。
type proc struct {
	mgr  *Manager
	spec config.ProcSpec

	mu       sync.Mutex
	state    string
	since    time.Time
	restarts int
	desired  bool // 期望运行（true=running）
	stopping bool // 退出由 stop/reload 指令触发（区别于崩溃）
	pid      int
	logs     *logRing // 运行日志环形缓冲（P1-11），stdout/stderr 镜像

	req    chan ctrlReq
	exited chan struct{} // 当前进程退出后关闭；启动失败时为已关闭通道
	kill   func()        // 终止当前进程

	// metrics 最近一次成功采集的指标快照
	metrics map[string]uint64
}

type ctrlReq struct {
	action string
	reply  chan error
}

// New 按规格表创建管理器（不启动进程，由 Start 统一监管）。
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

// Start 为每个进程拉起监管循环；agent 启动即自启全部进程。
func (m *Manager) Start(ctx context.Context) {
	for _, p := range m.procs {
		p.mu.Lock()
		p.desired = true
		p.mu.Unlock()
		// 二进制缺失预检（hy2 批）：启动前显式探测 exec 可执行文件，
		// 缺失立即发告警（面板可见明确原因），不等首轮 launch 失败——
		// 监管循环照常退避重试，装上二进制后自动拉起。
		if _, err := os.Stat(p.spec.Exec); err != nil {
			m.alarm(p, agentproto.AlarmKindProcCrash, agentproto.AlarmSeverityCritical,
				fmt.Sprintf("binary not found: %s (fix procs[].exec or install the engine)", p.spec.Exec))
		}
		go m.supervise(ctx, p)
		// SAVE-3：带指标接口（MetricsURL）的进程另起采集协程，
		// 结果挂状态快照随心跳上报。
		if p.spec.MetricsURL != "" {
			go m.collectProcMetrics(ctx, p)
		}
	}
}

// Statuses 返回全部进程状态快照（供心跳）。
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

// Control 执行面板指令：start/stop/reload，阻塞到指令落地或超时。
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

// Logs 返回进程运行日志的最近 limit 行（P1-11）；未知进程报错。
func (m *Manager) Logs(name string, limit int) ([]string, error) {
	m.mu.Lock()
	p := m.procs[name]
	m.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("unknown proc %q", name)
	}
	return p.logs.snapshot(limit), nil
}

// supervise 是单进程的监管循环：期望运行则保活，崩溃退避拉起，指令即时响应。
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
			// 启动失败按崩溃处理：退避后重试；告警带 exec 路径与底层
			// 错误（二进制缺失/权限等面板可见明确原因，不静默）。
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
				// stop/reload 触发的退出：回到循环头，desired 决定是否再拉起。
				continue
			}
			// 非预期退出：崩溃告警并退避拉起。
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

// backoffWait 退避等待，期间照常响应指令；返回下一段退避。
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

// launch 启动进程；无论成败都保证 p.exited/p.kill 可用，返回启动错误
// （nil=已拉起，进程退出另行经 exited 通道感知）。
func (m *Manager) launch(p *proc) error {
	cmd := exec.Command(p.spec.Exec, p.spec.Args...)
	cmd.Dir = p.spec.WorkDir
	// 运行日志镜像：agent 日志之外写入进程环形缓冲，供面板拉取（P1-11）。
	cmd.Stdout = io.MultiWriter(m.log.Writer(), p.logs)
	cmd.Stderr = io.MultiWriter(m.log.Writer(), p.logs)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

// applyWhenStopped 处理停机态指令，返回后续退避起点。
func (m *Manager) applyWhenStopped(p *proc, r ctrlReq) time.Duration {
	defer close(r.reply)
	switch r.action {
	case agentproto.ProcActionStart, agentproto.ProcActionReload:
		// 停机态 start 与 reload 等价：恢复期望运行。
		p.mu.Lock()
		p.desired = true
		p.mu.Unlock()
		r.reply <- nil
	case agentproto.ProcActionStop:
		// 停机态与崩溃退避态都可能出现 stop：统一置为不期望运行。
		p.mu.Lock()
		p.desired = false
		p.mu.Unlock()
		r.reply <- nil
	default:
		r.reply <- fmt.Errorf("unknown action %q", r.action)
	}
	return m.minBackoff
}

// handleRunningReq 处理运行态指令。
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

// reload 按 reload 策略执行：signal 发 SIGHUP，restart 停止后由监管循环拉起。
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
	<-p.exited // 监管循环随后按 desired=true 立即拉起
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

// consumeStopping 返回并清除 stopping 标记：退出是否由指令触发。
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

// collectProcMetrics 持续轮询单个进程的 MetricsURL 并缓存结果；
// 采集失败（不可达/非 200/解析错）清空快照，ctx 取消即退出。
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

// setMetrics 更新进程指标快照（p.mu 口径，与 status() 读侧一致）。
func (p *proc) setMetrics(metrics map[string]uint64) {
	p.mu.Lock()
	p.metrics = metrics
	p.mu.Unlock()
}
