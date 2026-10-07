// Package probe 边缘探测：按规格周期探测目标可达性、延迟与丢包
// （多轮拨号，过半失败判 sick）；direction=in 的回程探测在晚高峰加密。
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

// 探测口径：单次探测做 probeRounds 轮拨号（丢包=失败轮占比），
// 轮间停 roundSpacing；过半轮失败按不可达处置。
const (
	dialTimeout  = 3 * time.Second
	probeRounds  = 5
	roundSpacing = 200 * time.Millisecond
	sickLossPct  = 50

	// 晚高峰窗口与加密倍数（北京时间 19–23 时，固定 UTC+8 不依赖节点时区）：
	// 回国体验全在晚高峰，白天数据没有代表性（设计稿 §2.3）。
	peakHourStart = 19
	peakHourEnd   = 23
	peakDivisor   = 3
	minPeakGap    = 10 * time.Second
)

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
// 隧道探测（E-28 回国回程）直拨规格目标端口——与 relay 拨落地的
// 网络路径相同，但独立于 relay 进程（转发崩不带崩探测）。
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, spec := range r.specs {
		wg.Add(1)
		go func(spec config.ProbeSpec) {
			defer wg.Done()
			r.loop(ctx, spec)
		}(spec)
	}
	wg.Wait()
}

// loop 是单个规格的周期探测循环；下次间隔按晚高峰规则取。
func (r *Runner) loop(ctx context.Context, spec config.ProbeSpec) {
	t := time.NewTimer(0) // 启动即探一次
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.probeOnce(spec)
		t.Reset(nextInterval(spec, time.Now()))
	}
}

// probeOnce 执行一次多轮探测并上报结论；离线时静默丢弃。
func (r *Runner) probeOnce(spec config.ProbeSpec) {
	rtt, loss, reachable := measure(spec.Target)
	// 区域/运营商快照由面板按目标节点落库时补全。
	report := agentproto.ProbeReport{
		TargetKind: spec.TargetKind,
		TargetHost: spec.Target,
		Direction:  spec.Direction,
		RttMs:      rtt,
		LossPct:    loss,
		Reachable:  reachable,
		Verdict:    verdictOf(loss, reachable),
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

// measure 对目标做 probeRounds 轮 TCP 拨号：返回平均成功 RTT（毫秒）、
// 丢包率 0-100 与可达性（任一轮成功即可达）。
func measure(target string) (rttMs, lossPct int, reachable bool) {
	ok, sum := 0, 0
	for i := 0; i < probeRounds; i++ {
		if i > 0 {
			time.Sleep(roundSpacing)
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", target, dialTimeout)
		if err != nil {
			continue
		}
		_ = conn.Close()
		ok++
		sum += int(time.Since(start).Milliseconds())
	}
	if ok == 0 {
		return 0, 100, false
	}
	return sum / ok, (probeRounds - ok) * 100 / probeRounds, true
}

// verdictOf 按可达性与丢包定判定：不可达或过半轮失败即 sick——
// 晚高峰丢包过半即回程劣化，聚合要能按 sick 摘。
func verdictOf(lossPct int, reachable bool) string {
	if !reachable || lossPct >= sickLossPct {
		return agentproto.ProbeVerdictSick
	}
	return agentproto.ProbeVerdictHealthy
}

// nextInterval 返回下次探测间隔：direction=in 的回程探测在晚高峰
// （北京时间 19–23 时）加密为 base/3（下限 10s，且不慢于 base）。
func nextInterval(spec config.ProbeSpec, now time.Time) time.Duration {
	interval := time.Duration(spec.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if spec.Direction != agentproto.DirectionIn || !inPeak(now) {
		return interval
	}
	peak := interval / peakDivisor
	if peak < minPeakGap {
		peak = minPeakGap
	}
	if peak > interval {
		peak = interval // base 已低于下限，不倒退成更慢
	}
	return peak
}

// inPeak 判断是否处于晚高峰窗口：按北京时间（固定 UTC+8）。
func inPeak(now time.Time) bool {
	h := (now.UTC().Hour() + 8) % 24
	return h >= peakHourStart && h < peakHourEnd
}
