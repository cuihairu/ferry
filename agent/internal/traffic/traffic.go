// Package traffic 周期采集进程流量并上报（A-19/A-21）。
// 采集实现按 kind 注册：xray 的 gRPC stats 对接在 P1-3，P0 统一 Noop（标记未实现，
// 采集不到就不上报，避免零值噪音写进记账）。
package traffic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// errNotImplemented 标记该 kind 的采集尚未实现。
var errNotImplemented = errors.New("traffic collection not implemented for this kind")

// Collector 返回单个进程自上次查询的流量增量与在线连接数。
type Collector interface {
	Collect(proc string) (rx, tx uint64, conns int, err error)
}

// Noop 是不做任何事的空实现，对齐面板侧 xray.NoopHandler 的 P0 边界模式。
type Noop struct{}

func (Noop) Collect(string) (uint64, uint64, int, error) {
	return 0, 0, 0, errNotImplemented
}

// CollectorFor 按 kind 返回采集器；P0 全部 Noop，P1-3 起按 kind 接真实实现。
func CollectorFor(kind string) Collector {
	return Noop{}
}

// Dispatch 按进程规格把采集请求路由到对应 kind 的采集器。
func Dispatch(specs []config.ProcSpec) Collector {
	byProc := map[string]Collector{}
	for _, s := range specs {
		byProc[s.Name] = CollectorFor(s.Kind)
	}
	return dispatchCollector(byProc)
}

type dispatchCollector map[string]Collector

func (d dispatchCollector) Collect(proc string) (uint64, uint64, int, error) {
	c, ok := d[proc]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no collector for proc %q", proc)
	}
	return c.Collect(proc)
}

// Reporter 周期采集全部进程并批量上报。
type Reporter struct {
	log       *log.Logger
	interval  time.Duration
	procs     []string
	collector Collector

	sendMu sync.Mutex
	send   func(agentproto.Envelope) error // 当前连接的发送口；断开即清空

	// collectErr 记录每进程已报过的采集错误，同类错误只打一次日志。
	collectErr map[string]bool
}

// New 创建上报器。procs 为被管进程名列表，interval <=0 时关闭周期采集。
func New(specs []config.ProcSpec, interval time.Duration, collector Collector, logger *log.Logger) *Reporter {
	if logger == nil {
		logger = log.Default()
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	return &Reporter{
		log:        logger,
		interval:   interval,
		procs:      names,
		collector:  collector,
		collectErr: map[string]bool{},
	}
}

// SetSend 绑定当前连接的发送口。
func (r *Reporter) SetSend(send func(agentproto.Envelope) error) {
	r.sendMu.Lock()
	r.send = send
	r.sendMu.Unlock()
}

// Run 周期采集上报，直到 ctx 取消。
func (r *Reporter) Run(ctx context.Context) {
	if r.interval <= 0 {
		return
	}
	timer := time.NewTimer(r.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if items := r.collectAll(); len(items) > 0 {
			r.report(items)
		}
		timer.Reset(r.interval)
	}
}

// collectAll 采集全部进程；采集失败的进程跳过（同类错误只记一次日志）。
func (r *Reporter) collectAll() []agentproto.ProcTraffic {
	now := time.Now()
	items := make([]agentproto.ProcTraffic, 0, len(r.procs))
	for _, name := range r.procs {
		rx, tx, conns, err := r.collector.Collect(name)
		if err != nil {
			if !r.collectErr[name] {
				r.collectErr[name] = true
				r.log.Printf("collect traffic proc=%s: %v (won't repeat)", name, err)
			}
			continue
		}
		delete(r.collectErr, name)
		items = append(items, agentproto.ProcTraffic{
			Proc: name, Rx: rx, Tx: tx, Conns: conns, At: now,
		})
	}
	return items
}

// report 批量上报；离线时静默丢弃（周期性数据，下一轮再报）。
func (r *Reporter) report(items []agentproto.ProcTraffic) {
	r.sendMu.Lock()
	send := r.send
	r.sendMu.Unlock()
	if send == nil {
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("traffic-%d", time.Now().UnixNano()),
		agentproto.MsgTraffic,
		agentproto.TrafficReport{Items: items},
	)
	if err != nil {
		return
	}
	if err := send(env); err != nil {
		r.log.Printf("send traffic report: %v", err)
	}
}
