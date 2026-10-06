// Package aggregate 探测结论聚合判定：按区域/运营商维度判定故障。
// 判定在面板侧完成——探测在边缘节点本地执行，面板只收结论，
// 不集中探测（单点假死、网络视角失真）。
package aggregate

import (
	"sort"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 判定口径默认值：窗口 5 分钟、异常节点数阈值 2、异常占比阈值 50%。
const (
	DefaultWindow   = 5 * time.Minute
	DefaultMinSick  = 2
	DefaultSickRate = 0.5
)

// Dimension 是聚合维度。
type Dimension string

// 聚合维度取值。
const (
	DimensionRegion Dimension = "region" // 区域维度
	DimensionISP    Dimension = "isp"    // 运营商维度
)

// Verdict 是一个维度值的判定结论。
type Verdict struct {
	Dimension Dimension
	Scope     string   // 区域名或运营商名
	Sick      int      // 异常节点数
	Total     int      // 窗口内有结论的节点数
	SickNodes []uint64 // 异常节点 ID
	Failed    bool     // 是否判定故障
}

// Options 是判定口径，零值回退默认。
type Options struct {
	Window   time.Duration
	MinSick  int
	SickRate float64
}

func (o Options) normalize() Options {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.MinSick <= 0 {
		o.MinSick = DefaultMinSick
	}
	if o.SickRate <= 0 || o.SickRate > 1 {
		o.SickRate = DefaultSickRate
	}
	return o
}

// Judge 聚合窗口内隧道与互探结论，按区域与运营商两个维度判定故障。
// 每个目标节点取窗口内最新一条结论记为当前状态；
// 维度值判定故障口径（设计稿 §C.4.3）：异常节点数 ≥ MinSick 或异常占比 ≥ SickRate。
// 区域断网时整区域一起判失败，处置是整区域切走，不是逐个摘挂。
func Judge(db *gorm.DB, opts Options) ([]Verdict, error) {
	opts = opts.normalize()
	since := time.Now().Add(-opts.Window)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at >= ? AND target_kind IN ?",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return nil, err
	}

	// 每个目标节点取窗口内最新一条结论。
	latest := map[uint]storage.ProbeReport{}
	for _, r := range reports {
		if r.TargetNodeID == nil {
			continue // 出口基线无目标节点，不参与维度聚合
		}
		id := *r.TargetNodeID
		if prev, ok := latest[id]; !ok || r.ProbedAt.After(prev.ProbedAt) {
			latest[id] = r
		}
	}

	byDim := map[Dimension]map[string]*Verdict{
		DimensionRegion: {},
		DimensionISP:    {},
	}
	for id, r := range latest {
		record(byDim[DimensionRegion], DimensionRegion, r.Region, id, r)
		record(byDim[DimensionISP], DimensionISP, r.ISP, id, r)
	}

	out := make([]Verdict, 0, len(byDim[DimensionRegion])+len(byDim[DimensionISP]))
	for _, dim := range []Dimension{DimensionRegion, DimensionISP} {
		for _, v := range byDim[dim] {
			ratio := 0.0
			if v.Total > 0 {
				ratio = float64(v.Sick) / float64(v.Total)
			}
			v.Failed = v.Sick >= opts.MinSick || ratio >= opts.SickRate
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dimension != out[j].Dimension {
			return out[i].Dimension < out[j].Dimension
		}
		return out[i].Scope < out[j].Scope
	})
	return out, nil
}

// record 把一条目标节点结论计入维度聚合；nodeID 是目标节点。
func record(by map[string]*Verdict, dim Dimension, scope string, nodeID uint, r storage.ProbeReport) {
	if scope == "" {
		scope = "未知"
	}
	v, ok := by[scope]
	if !ok {
		v = &Verdict{Dimension: dim, Scope: scope}
		by[scope] = v
	}
	v.Total++
	if r.Verdict == agentproto.ProbeVerdictSick {
		v.Sick++
		v.SickNodes = append(v.SickNodes, uint64(nodeID))
		sort.Slice(v.SickNodes, func(i, j int) bool { return v.SickNodes[i] < v.SickNodes[j] })
	}
}
