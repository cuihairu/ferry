package handler

import (
	"sync"
	"time"
)

// 报告消息幂等（FERRY-004）：agent→panel 报告类消息按 (节点, 类型, envelope ID)
// 保证 at-most-once 副作用——重复帧跳过入库/记账但照常回 ack。定义与可执行判据见
// docs/design/agent架构设计.md §8。agent 现状发送失败即丢弃（无重传），本层是
// 契约面 + 防御层；空 ID 不去重（向前兼容旧 agent 的 proc_report）。
const (
	dedupeCap = 512              // 每节点窗口容量（FIFO，挤出即失效）
	dedupeTTL = 10 * time.Minute // 窗口条目存活时长，与节点令牌轮换宽限同量级
)

// reportDeduper 是单节点的幂等窗口：有界 FIFO + TTL，双界先到先失效。
// 生命周期挂 Handler（面板进程），跨 agent 重连保留，面板重启清零。
type reportDeduper struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	seen  map[string]time.Time
	order []string // FIFO 淘汰序；条目可能先被 TTL 路径删除，弹出时按 seen 实况
}

func newReportDeduper() *reportDeduper {
	return newReportDeduperCap(dedupeCap, dedupeTTL)
}

func newReportDeduperCap(capacity int, ttl time.Duration) *reportDeduper {
	return &reportDeduper{cap: capacity, ttl: ttl, seen: make(map[string]time.Time)}
}

// repeat 记录一帧并返回是否为窗口内的重复帧；空 ID 恒 false 且不记录。
func (d *reportDeduper) repeat(msgType, id string) bool {
	if id == "" {
		return false
	}
	key := msgType + "\x00" + id
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if at, ok := d.seen[key]; ok {
		if now.Sub(at) < d.ttl {
			return true
		}
		delete(d.seen, key) // TTL 过期：按新帧重新记录
	}
	for len(d.seen) >= d.cap {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	d.seen[key] = now
	d.order = append(d.order, key)
	return false
}

// repeatReport 记录一帧 agent 报告并返回是否为幂等窗口内的重复帧（按节点分桶）。
func (h *Handler) repeatReport(nodeID int64, msgType, id string) bool {
	if id == "" {
		return false
	}
	v, _ := h.dedupe.LoadOrStore(nodeID, newReportDeduper())
	return v.(*reportDeduper).repeat(msgType, id)
}
