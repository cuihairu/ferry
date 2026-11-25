// Package aggregate aggregates probe verdicts: failure is judged per region/ISP dimension.
// The judgement runs on the panel side - probing happens locally on edge nodes and the panel
// only receives verdicts, never probing centrally (single-node hang and skewed network views).
package aggregate

import (
	"sort"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Default judgement rules: 5-minute window, sick-node threshold 2, sick-ratio threshold 50%.
const (
	DefaultWindow   = 5 * time.Minute
	DefaultMinSick  = 2
	DefaultSickRate = 0.5
)

// Dimension is an aggregation dimension.
type Dimension string

// Aggregation dimension values.
const (
	DimensionRegion Dimension = "region" // region dimension
	DimensionISP    Dimension = "isp"    // ISP dimension
)

// Verdict is the judgement for one dimension value.
type Verdict struct {
	Dimension Dimension
	Scope     string   // region name or ISP name
	Sick      int      // number of sick nodes
	Total     int      // nodes with a verdict inside the window
	SickNodes []uint64 // sick node IDs
	Failed    bool     // whether judged as failed
}

// Options holds the judgement rules; zero values fall back to defaults.
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

// Judge aggregates tunnel and peer-probe verdicts inside the window and judges failures on the
// region and ISP dimensions. Each target node takes its latest verdict in the window as its current state;
// a dimension value fails when sick nodes >= MinSick or the sick ratio >= SickRate (design §C.4.3).
// When a region loses connectivity the whole region fails together; the remedy is switching the region away, not suspending nodes one by one.
func Judge(db *gorm.DB, opts Options) ([]Verdict, error) {
	opts = opts.normalize()
	since := time.Now().Add(-opts.Window)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at >= ? AND target_kind IN ?",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return nil, err
	}

	// Each target node takes its latest verdict inside the window.
	latest := map[uint]storage.ProbeReport{}
	for _, r := range reports {
		if r.TargetNodeID == nil {
			continue // exit baseline has no target node and does not take part in dimension aggregation
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

// record counts one target-node verdict into the dimension aggregation; nodeID is the target node.
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
