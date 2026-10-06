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

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/configd"
	"github.com/cuihairu/ferry/agent/internal/host"
	"github.com/cuihairu/ferry/agent/internal/link"
	"github.com/cuihairu/ferry/agent/internal/probe"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/packages/agentproto"
)

const (
	helloTimeout = 10 * time.Second
	sendTimeout  = 10 * time.Second
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

	sendMu  sync.Mutex
	curSend func(agentproto.Envelope) error // 当前连接的发送口；断开即清空

	hbSeq int
	hello chan agentproto.Envelope

	// Procs 返回进程状态快照（心跳载荷用）。
	Procs func() []agentproto.ProcStatus
}

// New 创建 app，并按配置装配进程管理器。
func New(cfg config.Config, version string) *App {
	a := &App{
		cfg:      cfg,
		version:  version,
		log:      log.New(os.Stderr, "agent ", log.LstdFlags),
		interval: cfg.HeartbeatInterval(),
	}
	a.mgr = procs.New(cfg.Procs, a.log)
	a.mgr.OnStatusChange = a.reportStatus
	a.mgr.OnAlarm = a.reportAlarm
	a.Procs = a.mgr.Statuses
	a.cfgd = configd.New(a.log)
	a.probe = probe.New(cfg.Probes, a.log)
	a.probe.SetSend(a.sendIfConnected)
	return a
}

// Run 阻塞运行到 ctx 取消：先起进程监管与边缘探测，再维持与面板的连接。
func (a *App) Run(ctx context.Context) error {
	a.mgr.Start(ctx)
	go a.probe.Run(ctx)
	client := link.New(link.Options{
		URL:      a.cfg.PanelURL,
		CAFile:   a.cfg.TLS.CAFile,
		CertFile: a.cfg.TLS.CertFile,
		KeyFile:  a.cfg.TLS.KeyFile,
		Log:      a.log,
	})
	return client.Run(ctx, a)
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
	case agentproto.MsgConfigPush:
		// 下发执行耗时（校验命令 + reload），放后台跑避免阻塞读循环；串行化由 Deployer 保证。
		go a.handleConfigPush(env)
	case agentproto.MsgAlarmAck, agentproto.MsgTrafficAck:
		// 面板对 agent 上报的确认，无需处理。
	default:
		a.log.Printf("unknown message type %q", env.Type)
	}
}

// OnDisconnected 停掉本轮心跳并清空发送口。
func (a *App) OnDisconnected() {
	a.stopHeartbeat()
	a.setSend(nil)
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
		Procs:         []agentproto.ProcStatus{},
		At:            time.Now(),
	}
	if a.Procs != nil {
		hb.Procs = a.Procs()
	}
	return hb
}

var _ link.Handlers = (*App)(nil)
