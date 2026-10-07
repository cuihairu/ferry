// Package transport 区域传输判定（E-17）：把区域内各传输形态节点的
// 探测结论聚合出「哪种活着用哪种」——存活占比最高的传输即该区域推荐
// 传输；与区域现行主流传输不一致时给切换建议。切换 = 换配置不改架构
// （改节点 transport 标注并重推对应配置，复用 config.push 链路）。
package transport

import (
	"sort"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// DefaultWindow 判定窗口默认值，与区域聚合判定同口径。
const DefaultWindow = 5 * time.Minute

// Options 是判定口径，零值回退默认。
type Options struct {
	Window time.Duration
}

func (o Options) normalize() Options {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	return o
}

// TransportHealth 是一种传输在区域内的存活概要。
type TransportHealth struct {
	Transport string `json:"transport"`
	Total     int    `json:"total"` // 窗口内有新鲜结论的节点数
	Alive     int    `json:"alive"` // 最新结论 healthy 的节点数
}

// Row 是一个区域的传输判定结论。
type Row struct {
	Region      string            `json:"region"`
	Transports  []TransportHealth `json:"transports"`
	Current     string            `json:"current"`      // 区域现行主流传输（节点数最多）
	Recommended string            `json:"recommended"`  // 存活占比最高的传输
	Switch      bool              `json:"switchneeded"` // 建议 != 现行，建议换线
}

// Judge 聚合窗口内互探/隧道结论，按区域×传输给判定（E-17）。
// 每个目标节点取最新一条结论记存活；无新鲜结论的节点不参与。
func Judge(db *gorm.DB, opts Options) ([]Row, error) {
	opts = opts.normalize()
	since := time.Now().Add(-opts.Window)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at >= ? AND target_kind IN ?",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return nil, err
	}
	// 每个目标节点取最新结论。
	latest := map[uint]storage.ProbeReport{}
	for _, r := range reports {
		if r.TargetNodeID == nil {
			continue
		}
		id := *r.TargetNodeID
		if prev, ok := latest[id]; !ok || r.ProbedAt.After(prev.ProbedAt) {
			latest[id] = r
		}
	}
	if len(latest) == 0 {
		return []Row{}, nil
	}
	// 目标节点的区域与传输标注。
	var nodes []storage.Node
	if err := db.Where("id IN ?", keys(latest)).Find(&nodes).Error; err != nil {
		return nil, err
	}
	byRegion := map[string]map[string]*TransportHealth{}
	nodeByID := make(map[uint]storage.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	for id, r := range latest {
		n, ok := nodeByID[id]
		if !ok || n.Transport == "" {
			continue
		}
		region := n.Region
		if region == "" {
			region = "未知"
		}
		tx, ok := byRegion[region]
		if !ok {
			tx = map[string]*TransportHealth{}
			byRegion[region] = tx
		}
		h, ok := tx[n.Transport]
		if !ok {
			h = &TransportHealth{Transport: n.Transport}
			tx[n.Transport] = h
		}
		h.Total++
		if r.Verdict == agentproto.ProbeVerdictHealthy {
			h.Alive++
		}
	}

	regions := make([]string, 0, len(byRegion))
	for region := range byRegion {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	out := make([]Row, 0, len(regions))
	for _, region := range regions {
		tx := byRegion[region]
		healths := make([]TransportHealth, 0, len(tx))
		for _, h := range tx {
			healths = append(healths, *h)
		}
		sort.Slice(healths, func(i, j int) bool { return healths[i].Transport < healths[j].Transport })
		row := Row{Region: region, Transports: healths}
		for _, h := range healths {
			// 现行主流：节点数最多（同数取传输名字典序，保证稳定）。
			if cur := findHealth(healths, row.Current); cur == nil || h.Total > cur.Total {
				row.Current = h.Transport
			}
			// 推荐：存活占比最高（同占比比存活数，再比字典序）。
			if rec := findHealth(healths, row.Recommended); rec == nil || better(h, *rec) {
				row.Recommended = h.Transport
			}
		}
		row.Switch = row.Recommended != row.Current
		out = append(out, row)
	}
	return out, nil
}

func findHealth(hs []TransportHealth, name string) *TransportHealth {
	for i := range hs {
		if hs[i].Transport == name {
			return &hs[i]
		}
	}
	return nil
}

// better 比较 a 是否比 b 更值得推荐：存活占比优先，同占比比存活数。
func better(a, b TransportHealth) bool {
	ra := float64(a.Alive) / float64(a.Total)
	rb := float64(b.Alive) / float64(b.Total)
	if ra != rb {
		return ra > rb
	}
	if a.Alive != b.Alive {
		return a.Alive > b.Alive
	}
	return a.Transport < b.Transport
}

func keys(m map[uint]storage.ProbeReport) []uint {
	out := make([]uint, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}
