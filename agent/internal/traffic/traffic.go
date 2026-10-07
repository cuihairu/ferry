// Package traffic 周期采集进程流量并上报（A-19/A-21）。
// 采集实现按 kind 注册：xray 走 gRPC stats 采集真实用户流量（P1-3），
// 其余 kind 仍 Noop（标记未实现，采集不到就不上报，避免零值噪音写进记账）。
package traffic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/xraystats"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// errNotImplemented 标记该 kind 的采集尚未实现。
var errNotImplemented = errors.New("traffic collection not implemented for this kind")

// Collector 返回单个进程自上次查询的流量增量与在线连接数。
type Collector interface {
	Collect(proc string) (rx, tx uint64, conns int, err error)
}

// UserCollector 能按用户拆分流量增量的采集器（P1-3 xray gRPC stats）；
// 实现者存在时 Reporter 优先走 per-user 路径，节点级由 per-user 求和。
type UserCollector interface {
	Collector
	CollectUsers(proc string) ([]agentproto.UserTraffic, error)
}

// BlockCollector 能采集被拦截流量增量的采集器（SAVE-4 广告/追踪拦截，
// xray block 出站字节）；实现者存在时 Reporter 在同周期附带拦截增量，
// 报错按缺失处理（拦截计数不影响流量记账主流程）。
type BlockCollector interface {
	CollectBlocked(proc string) (uint64, error)
}

// Noop 是不做任何事的空实现，对齐面板侧 xray.NoopHandler 的 P0 边界模式。
type Noop struct{}

func (Noop) Collect(string) (uint64, uint64, int, error) {
	return 0, 0, 0, errNotImplemented
}

// CollectorFor 按 kind 返回采集器；未对接的 kind 返回 Noop。
func CollectorFor(kind string) Collector {
	return Noop{}
}

// xray 采集器满足 per-user 口径（编译期断言）。
var _ UserCollector = (*xraystats.Collector)(nil)

// Dispatch 按进程规格把采集请求路由到对应 kind 的采集器。
func Dispatch(specs []config.ProcSpec) Collector {
	byProc := map[string]Collector{}
	for _, s := range specs {
		if s.Kind == "xray" && s.StatsAPI != "" {
			byProc[s.Name] = xraystats.NewCollector(s.StatsAPI, nil)
			continue
		}
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

// CollectUsers 把 per-user 采集路由到内部的 UserCollector；
// 该 proc 未接真实采集（Noop）时返回未实现，调用方按跳过处理。
func (d dispatchCollector) CollectUsers(proc string) ([]agentproto.UserTraffic, error) {
	uc, ok := d[proc].(UserCollector)
	if !ok {
		return nil, errNotImplemented
	}
	return uc.CollectUsers(proc)
}

var _ UserCollector = dispatchCollector{}

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
// 采集器支持 per-user 口径时优先走 CollectUsers，节点级增量由 per-user 求和
// （避免为节点级再查一次 reset 统计，把增量清成零）。
func (r *Reporter) collectAll() []agentproto.ProcTraffic {
	now := time.Now()
	items := make([]agentproto.ProcTraffic, 0, len(r.procs))
	for _, name := range r.procs {
		if uc, ok := r.collector.(UserCollector); ok {
			item, err := collectUsers(uc, name, now)
			if err != nil {
				r.noteCollectErr(name, err)
				continue
			}
			r.attachBlocked(&item)
			delete(r.collectErr, name)
			items = append(items, item)
			continue
		}
		rx, tx, conns, err := r.collector.Collect(name)
		if err != nil {
			r.noteCollectErr(name, err)
			continue
		}
		delete(r.collectErr, name)
		item := agentproto.ProcTraffic{
			Proc: name, Rx: rx, Tx: tx, Conns: conns, At: now,
		}
		r.attachBlocked(&item)
		items = append(items, item)
	}
	return items
}

// attachBlocked 给流量行附带本周期拦截增量（SAVE-4）：采集器不支持或
// 查询报错时保持缺省，不拖累流量记账。
func (r *Reporter) attachBlocked(item *agentproto.ProcTraffic) {
	bc, ok := r.collector.(BlockCollector)
	if !ok {
		return
	}
	if n, err := bc.CollectBlocked(item.Proc); err == nil {
		item.BlockedBytes = n
	}
}

// collectUsers 采集 per-user 增量并把节点级增量求和。
func collectUsers(uc UserCollector, name string, now time.Time) (agentproto.ProcTraffic, error) {
	users, err := uc.CollectUsers(name)
	if err != nil {
		return agentproto.ProcTraffic{}, err
	}
	item := agentproto.ProcTraffic{Proc: name, At: now}
	for _, u := range users {
		item.Rx += u.Rx
		item.Tx += u.Tx
	}
	item.Users = users
	return item, nil
}

// noteCollectErr 记录采集错误：同类只打一次日志。
func (r *Reporter) noteCollectErr(name string, err error) {
	if !r.collectErr[name] {
		r.collectErr[name] = true
		r.log.Printf("collect traffic proc=%s: %v (won't repeat)", name, err)
	}
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
