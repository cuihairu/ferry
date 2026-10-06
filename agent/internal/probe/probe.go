// Package probe 边缘探测：按规格周期探测目标可达性与延迟。
// 探测在节点本地完成，结论经 probe.report 上报面板，
// 面板只收结论做聚合判定，不集中探测。
package probe

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// dialTimeout 是单次探测拨号超时。
const dialTimeout = 3 * time.Second

// Runner 按规格周期执行边缘探测。
type Runner struct {
	log   *log.Logger
	specs []config.ProbeSpec

	sendMu sync.Mutex
	send   func(agentproto.Envelope) error // 当前连接发送口
}

// New 创建探测运行器。
func New(specs []config.ProbeSpec, logger *log.Logger) *Runner {
	if logger == nil {
		logger = log.Default()
	}
	return &Runner{log: logger, specs: specs}
}

// SetSend 设置上报发送口；agent 重连后由 app 更新。
func (r *Runner) SetSend(send func(agentproto.Envelope) error) {
	r.sendMu.Lock()
	r.send = send
	r.sendMu.Unlock()
}

// Run 阻塞运行到 ctx 取消：每个规格一个探测循环。
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, spec := range r.specs {
		if spec.TargetKind == agentproto.ProbeTargetTunnel {
			// 隧道探测经 relay 数据面（E-5/E-6 接入），当前跳过。
			r.log.Printf("probe %s: tunnel probe waits for relay, skipped", spec.Name)
			continue
		}
		wg.Add(1)
		go func(spec config.ProbeSpec) {
			defer wg.Done()
			r.loop(ctx, spec)
		}(spec)
	}
	wg.Wait()
}

// loop 是单个规格的周期探测循环。
func (r *Runner) loop(ctx context.Context, spec config.ProbeSpec) {
	interval := time.Duration(spec.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTimer(0) // 启动即探一次
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.probeOnce(spec)
		t.Reset(interval)
	}
}

// probeOnce 执行一次探测并上报结论；离线时静默丢弃。
func (r *Runner) probeOnce(spec config.ProbeSpec) {
	start := time.Now()
	reachable := dialReachable(spec.Target)
	rtt := int(time.Since(start).Milliseconds())
	verdict := agentproto.ProbeVerdictHealthy
	if !reachable {
		verdict = agentproto.ProbeVerdictSick
	}
	// 区域/运营商快照由面板按目标节点落库时补全。
	report := agentproto.ProbeReport{
		TargetKind: spec.TargetKind,
		TargetHost: spec.Target,
		Direction:  spec.Direction,
		RttMs:      rtt,
		Reachable:  reachable,
		Verdict:    verdict,
		ProbedAt:   time.Now(),
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("probe-%d", time.Now().UnixNano()),
		agentproto.MsgProbeReport,
		agentproto.ProbeReportBatch{Items: []agentproto.ProbeReport{report}},
	)
	if err != nil {
		return
	}
	r.sendMu.Lock()
	send := r.send
	r.sendMu.Unlock()
	if send == nil {
		return
	}
	if err := send(env); err != nil {
		r.log.Printf("probe %s report: %v", spec.Name, err)
	}
}

// dialReachable 探测 TCP 目标可达性。
func dialReachable(target string) bool {
	conn, err := net.DialTimeout("tcp", target, dialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
