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
	"github.com/cuihairu/ferry/agent/internal/host"
	"github.com/cuihairu/ferry/agent/internal/link"
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

	mu       sync.Mutex
	hbStop   chan struct{}
	interval time.Duration

	hbSeq int
	hello chan agentproto.Envelope

	// Procs 返回进程状态快照；进程管理功能接入后注入，nil 时心跳里为空。
	Procs func() []agentproto.ProcStatus
}

// New 创建 app。
func New(cfg config.Config, version string) *App {
	return &App{
		cfg:      cfg,
		version:  version,
		log:      log.New(os.Stderr, "agent ", log.LstdFlags),
		interval: cfg.HeartbeatInterval(),
	}
}

// Run 阻塞运行到 ctx 取消。
func (a *App) Run(ctx context.Context) error {
	client := link.New(link.Options{
		URL:      a.cfg.PanelURL,
		CAFile:   a.cfg.TLS.CAFile,
		CertFile: a.cfg.TLS.CertFile,
		KeyFile:  a.cfg.TLS.KeyFile,
		Log:      a.log,
	})
	return client.Run(ctx, a)
}

// OnConnected 发送 hello 并等待应答，超时视为握手失败。
func (a *App) OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error {
	a.stopHeartbeat()
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
		if err := ack.Decode(&ha); err == nil && ha.HeartbeatIntervalSec > 0 {
			a.setInterval(time.Duration(ha.HeartbeatIntervalSec) * time.Second)
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
	case agentproto.MsgConfigPush, agentproto.MsgProcCtl:
		// 配置下发与进程控制在后续批次接入，先记录保证协议前向兼容。
		a.log.Printf("received %s (id=%s): handler not enabled yet", env.Type, env.ID)
	case agentproto.MsgAlarmAck, agentproto.MsgTrafficAck:
		// 面板对 agent 上报的确认，无需处理。
	default:
		a.log.Printf("unknown message type %q", env.Type)
	}
}

// OnDisconnected 停掉本轮心跳。
func (a *App) OnDisconnected() {
	a.stopHeartbeat()
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
		MemUsedBytes:  s.MemUsedBytes,
		MemTotalBytes: s.MemTotalBytes,
		Procs:         []agentproto.ProcStatus{},
		At:            time.Now(),
	}
	if a.Procs != nil {
		hb.Procs = a.Procs()
	}
	return hb
}

var _ link.Handlers = (*App)(nil)
