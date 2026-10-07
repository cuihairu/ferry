// Package app 组装 agent 业务：握手 hello、周期心跳与消息分发。
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/certwatch"
	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/configd"
	"github.com/cuihairu/ferry/agent/internal/host"
	"github.com/cuihairu/ferry/agent/internal/link"
	"github.com/cuihairu/ferry/agent/internal/probe"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/agent/internal/roles/speedtest"
	"github.com/cuihairu/ferry/agent/internal/spool"
	"github.com/cuihairu/ferry/agent/internal/traffic"
	"github.com/cuihairu/ferry/packages/agentproto"
)

const (
	helloTimeout = 10 * time.Second
	sendTimeout  = 10 * time.Second

	highLoadCPU      = 90.0             // CPU 使用率告警阈值
	highLoadStreak   = 2                // 连续超阈值的心跳次数
	highLoadCooldown = 30 * time.Minute // 同类告警最短间隔
)

// App 实现 link.Handlers，持有运行态。
type App struct {
	cfg     config.Config
	version string
	log     *log.Logger
	mgr     *procs.Manager
	cfgd    *configd.Deployer

	mu       sync.Mutex
	hbStop   chan struct{}
	interval time.Duration

	probe *probe.Runner
	traf  *traffic.Reporter

	// 告警节流状态（A-7）：证书按「域名+有效期」只报一次，高负载按冷却期。
	alarmMu    sync.Mutex
	certAlarms map[string]time.Time
	lw         loadWatch

	sendMu  sync.Mutex
	curSend func(agentproto.Envelope) error // 当前连接的发送口；断开即清空

	// calibrated 保证测速校准每进程只跑一次（重连不重测，E-8）。
	calMu      sync.Mutex
	calibrated bool

	hbSeq int
	hello chan agentproto.Envelope

	// Procs 返回进程状态快照（心跳载荷用）。
	Procs func() []agentproto.ProcStatus
}

// New 创建 app，并按配置装配进程管理器。
func New(cfg config.Config, version string) *App {
	a := &App{
		cfg:        cfg,
		version:    version,
		log:        log.New(os.Stderr, "agent ", log.LstdFlags),
		interval:   cfg.HeartbeatInterval(),
		certAlarms: map[string]time.Time{},
	}
	a.mgr = procs.New(cfg.Procs, a.log)
	a.mgr.OnStatusChange = a.reportStatus
	a.mgr.OnAlarm = a.reportAlarm
	a.Procs = a.mgr.Statuses
	a.cfgd = configd.New(a.log)
	a.probe = probe.New(cfg.Probes, a.log)
	a.probe.SetSend(a.sendIfConnected)
	a.traf = traffic.New(cfg.Procs, cfg.TrafficInterval(), traffic.Dispatch(cfg.Procs), a.log)
	return a
}

// Run 阻塞运行到 ctx 取消：先起进程监管与边缘探测，再维持与面板的连接。
func (a *App) Run(ctx context.Context) error {
	a.mgr.Start(ctx)
	if a.cfg.SpoolDir != "" {
		// 角色模式：探测由 -role probe 独立进程跑，经 spool 上报；
		// 核心不再跑内置探测，避免结论重复。
		go func() {
			_ = spool.Watch(ctx, a.cfg.SpoolDir, a.sendIfConnected)
		}()
	} else {
		go a.probe.Run(ctx)
	}
	go a.traf.Run(ctx)
	client := link.New(link.Options{
		URL:      a.cfg.PanelURL,
		CAFile:   a.cfg.TLS.CAFile,
		CertFile: a.cfg.TLS.CertFile,
		KeyFile:  a.cfg.TLS.KeyFile,
		Log:      a.log,
	})
	return client.Run(ctx, a)
}

// maybeCalibrate 注册后跑一次轻量测速并上报（E-8）：无套餐下行容量即跳过
// （纯中转/未定价节点不测）；测速失败只记日志，不影响在线。
func (a *App) maybeCalibrate(send func(agentproto.Envelope) error) {
	a.calMu.Lock()
	if a.calibrated {
		a.calMu.Unlock()
		return
	}
	a.calibrated = true
	a.calMu.Unlock()

	plan := a.cfg.Meta.BwDownMbps
	if plan <= 0 {
		return
	}
	base, err := speedtest.BaseURL(a.cfg.PanelURL)
	if err != nil {
		a.log.Printf("calibrate: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), speedtest.Timeout)
	defer cancel()
	res, err := speedtest.Measure(ctx, base, speedtest.DefaultBytes)
	if err != nil {
		a.log.Printf("calibrate: measure: %v", err)
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("cal-%d", time.Now().UnixNano()),
		agentproto.MsgCalibrate,
		agentproto.Calibrate{
			MeasuredDownMbps: res.Mbps,
			Bytes:            res.Bytes,
			Seconds:          res.Seconds,
		},
	)
	if err != nil {
		return
	}
	if err := send(env); err != nil {
		a.log.Printf("calibrate: send: %v", err)
	}
}

// reportStatus 把进程状态变化推给面板（离线时静默丢弃）。
func (a *App) reportStatus(status agentproto.ProcStatus) {
	env, err := agentproto.NewEnvelope("", agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{status},
	})
	if err != nil {
		return
	}
	_ = a.sendIfConnected(env)
}

// reportAlarm 上报异常告警。
func (a *App) reportAlarm(al agentproto.Alarm) {
	env, err := agentproto.NewEnvelope(fmt.Sprintf("alarm-%d", time.Now().UnixNano()), agentproto.MsgAlarm, al)
	if err != nil {
		return
	}
	_ = a.sendIfConnected(env)
}

func (a *App) sendIfConnected(env agentproto.Envelope) error {
	a.sendMu.Lock()
	send := a.curSend
	a.sendMu.Unlock()
	if send == nil {
		return errors.New("not connected")
	}
	return send(env)
}

func (a *App) setSend(send func(agentproto.Envelope) error) {
	a.sendMu.Lock()
	a.curSend = send
	a.sendMu.Unlock()
}

// OnConnected 发送 hello 并等待应答，超时视为握手失败。
func (a *App) OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error {
	a.stopHeartbeat()
	a.setSend(send)
	a.traf.SetSend(send)
	ch := make(chan agentproto.Envelope, 1)
	a.mu.Lock()
	a.hello = ch
	a.mu.Unlock()

	hostname, _ := os.Hostname()
	env, err := agentproto.NewEnvelope("hello", agentproto.MsgHello, agentproto.Hello{
		Token:     a.cfg.Token,
		AgentID:   a.cfg.AgentID,
		Version:   a.version,
		Hostname:  hostname,
		StartedAt: time.Now(),
		Meta:      a.cfg.Meta,
	})
	if err != nil {
		return err
	}
	a.log.Printf("connected, sending hello (agent_id=%s)", a.cfg.AgentID)
	if err := send(env); err != nil {
		return err
	}
	select {
	case ack := <-ch:
		var ha agentproto.HelloAck
		if err := ack.Decode(&ha); err == nil {
			if ha.HeartbeatIntervalSec > 0 {
				a.setInterval(time.Duration(ha.HeartbeatIntervalSec) * time.Second)
			}
			// 面板为元数据权威来源：hello_ack 回传值覆盖本地初值。
			if ha.Meta.Role != "" || ha.Meta.ISP != "" {
				ha.Meta.Normalize()
				a.cfg.Meta = ha.Meta
			}
		}
		a.startHeartbeat(ctx, send)
		go a.maybeCalibrate(send)
		return nil
	case <-time.After(helloTimeout):
		return errors.New("hello ack timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OnMessage 分发面板消息。
func (a *App) OnMessage(_ context.Context, env agentproto.Envelope, send func(agentproto.Envelope) error) {
	switch env.Type {
	case agentproto.MsgHelloAck:
		a.mu.Lock()
		ch := a.hello
		a.mu.Unlock()
		if ch != nil {
			select {
			case ch <- env:
			default:
			}
		}
	case agentproto.MsgHeartbeatAck:
		var ha agentproto.HeartbeatAck
		if err := env.Decode(&ha); err == nil && ha.NextIntervalSec > 0 {
			a.setInterval(time.Duration(ha.NextIntervalSec) * time.Second)
		}
	case agentproto.MsgProcCtl:
		var ctl agentproto.ProcCtl
		ack := agentproto.ProcCtlAck{Proc: ctl.Proc, Action: ctl.Action}
		if err := env.Decode(&ctl); err != nil {
			ack.OK = false
			ack.Error = err.Error()
		} else {
			ack.Proc = ctl.Proc
			ack.Action = ctl.Action
			if err := a.mgr.Control(ctl.Proc, ctl.Action); err != nil {
				ack.OK = false
				ack.Error = err.Error()
			} else {
				ack.OK = true
			}
		}
		reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProcCtlAck, ack)
		_ = a.sendIfConnected(reply)
	case agentproto.MsgProcLogs:
		// 进程日志拉取（P1-11）：从进程环形缓冲回最近 limit 行。
		var req agentproto.ProcLogsReq
		ack := agentproto.ProcLogsAck{}
		if err := env.Decode(&req); err != nil {
			ack.Error = err.Error()
		} else {
			ack.Proc = req.Proc
			if req.Limit <= 0 {
				req.Limit = 200
			}
			if req.Limit > 1000 {
				req.Limit = 1000
			}
			lines, err := a.mgr.Logs(req.Proc, req.Limit)
			if err != nil {
				ack.Error = err.Error()
			} else {
				ack.Lines = lines
			}
		}
		reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProcLogsAck, ack)
		_ = a.sendIfConnected(reply)
	case agentproto.MsgConfigPush:
		// 下发执行耗时（校验命令 + reload），放后台跑避免阻塞读循环；串行化由 Deployer 保证。
		go a.handleConfigPush(env)
	case agentproto.MsgAlarmAck, agentproto.MsgTrafficAck:
		// 面板对 agent 上报的确认，无需处理。
	case agentproto.MsgCalibrateAck:
		var ca agentproto.CalibrateAck
		if err := env.Decode(&ca); err == nil {
			a.log.Printf("calibrate ack: accepted=%v effective=%dMbps (%s)", ca.Accepted, ca.EffectiveDownMbps, ca.Note)
		}
	default:
		a.log.Printf("unknown message type %q", env.Type)
	}
}

// OnDisconnected 停掉本轮心跳并清空发送口。
func (a *App) OnDisconnected() {
	a.stopHeartbeat()
	a.setSend(nil)
	a.traf.SetSend(nil)
}

// handleConfigPush 执行配置下发并回 config.ack（A-16/A-17）。
func (a *App) handleConfigPush(env agentproto.Envelope) {
	var push agentproto.ConfigPush
	if err := env.Decode(&push); err != nil {
		a.log.Printf("config_push decode: %v", err)
		return // 无法定位 proc/version，ack 无从构造，放弃
	}
	ack := agentproto.ConfigAck{Proc: push.Proc, Version: push.Version}
	spec, ok := a.procSpec(push.Proc)
	if !ok {
		ack.Error = fmt.Sprintf("unknown proc %q", push.Proc)
	} else {
		ack = a.cfgd.Apply(spec, push, func() error {
			return a.mgr.Control(push.Proc, agentproto.ProcActionReload)
		})
	}
	reply, err := agentproto.NewEnvelope(env.ID, agentproto.MsgConfigAck, ack)
	if err != nil {
		return
	}
	if err := a.sendIfConnected(reply); err != nil {
		a.log.Printf("send config_ack: %v", err)
	}
}

// procSpec 按名字查找进程规格。
func (a *App) procSpec(name string) (config.ProcSpec, bool) {
	for _, p := range a.cfg.Procs {
		if p.Name == name {
			return p, true
		}
	}
	return config.ProcSpec{}, false
}

func (a *App) setInterval(d time.Duration) {
	a.mu.Lock()
	a.interval = d
	a.mu.Unlock()
}

func (a *App) currentInterval() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interval
}

func (a *App) startHeartbeat(ctx context.Context, send func(agentproto.Envelope) error) {
	stop := make(chan struct{})
	a.mu.Lock()
	a.hbStop = stop
	a.mu.Unlock()

	go func() {
		timer := time.NewTimer(a.currentInterval())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
			}
			a.mu.Lock()
			a.hbSeq++
			seq := a.hbSeq
			a.mu.Unlock()

			env, err := agentproto.NewEnvelope(fmt.Sprintf("hb-%d", seq), agentproto.MsgHeartbeat, a.buildHeartbeat())
			if err == nil {
				if err := send(env); err != nil {
					a.log.Printf("send heartbeat: %v", err)
					return
				}
			}
			timer.Reset(a.currentInterval())
		}
	}()
}

func (a *App) stopHeartbeat() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hbStop != nil {
		close(a.hbStop)
		a.hbStop = nil
	}
}

func (a *App) buildHeartbeat() agentproto.Heartbeat {
	s := host.Sample()
	hb := agentproto.Heartbeat{
		UptimeSec:     s.UptimeSec,
		Load1:         s.Load1,
		CPUUtil:       s.CPUUtil,
		MemUsedBytes:  s.MemUsedBytes,
		MemTotalBytes: s.MemTotalBytes,
		NetRxBytes:    s.NetRxBytes,
		NetTxBytes:    s.NetTxBytes,
		Conns:         s.TCPConns,
		Certs:         a.collectCerts(),
		Procs:         []agentproto.ProcStatus{},
		At:            time.Now(),
	}
	if a.Procs != nil {
		hb.Procs = a.Procs()
	}
	a.watchAlarms(hb)
	return hb
}

// collectCerts 汇总受管进程配置里的证书状态（A-3）。
func (a *App) collectCerts() []agentproto.CertStatus {
	out := []agentproto.CertStatus{}
	for _, p := range a.cfg.Procs {
		if p.ConfigPath == "" {
			continue
		}
		out = append(out, certwatch.FromFile(p.ConfigPath)...)
	}
	return out
}

// watchAlarms 基于心跳采样触发证书临期与持续高负载告警（A-7），内部自带节流。
func (a *App) watchAlarms(hb agentproto.Heartbeat) {
	now := time.Now()
	a.alarmMu.Lock()
	defer a.alarmMu.Unlock()
	for _, c := range certwatch.Expiring(hb.Certs, now, certwatch.ExpiryWindow) {
		// 同一张证书同一有效期只报一次，续期后（NotAfter 变化）重新具备告警资格。
		if last, ok := a.certAlarms[c.Domain]; ok && last.Equal(c.NotAfter) {
			continue
		}
		a.certAlarms[c.Domain] = c.NotAfter
		msg := fmt.Sprintf("证书 %s 将于 %s 到期", c.Domain, c.NotAfter.Format("2006-01-02"))
		if ttl := time.Until(c.NotAfter); ttl < 0 {
			msg = fmt.Sprintf("证书 %s 已于 %s 过期", c.Domain, c.NotAfter.Format("2006-01-02"))
		} else {
			msg += fmt.Sprintf("（剩 %d 天）", int(ttl.Hours()/24))
		}
		a.reportAlarm(agentproto.Alarm{
			Kind: agentproto.AlarmKindCertExpiry, Severity: "warn", Message: msg, At: now,
		})
	}
	if a.lw.observe(hb.CPUUtil, now) {
		a.reportAlarm(agentproto.Alarm{
			Kind: agentproto.AlarmKindHighLoad, Severity: "warn",
			Message: fmt.Sprintf("CPU 使用率持续 ≥ %.0f%%", highLoadCPU), At: now,
		})
	}
}

// loadWatch 连续高负载判定：连续 streak 次超阈值报警一次，冷却期内不重报。
type loadWatch struct {
	streak int
	last   time.Time
}

func (w *loadWatch) observe(cpu float64, now time.Time) bool {
	if cpu < highLoadCPU {
		w.streak = 0
		return false
	}
	w.streak++
	if w.streak < highLoadStreak {
		return false
	}
	if now.Sub(w.last) < highLoadCooldown {
		return false
	}
	w.last = now
	w.streak = 0
	return true
}

var _ link.Handlers = (*App)(nil)
