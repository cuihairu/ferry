// Package transport is regional transport judgment (E-17): it aggregates the probe conclusions of
// each transport form's nodes within a region to conclude "which form is alive and usable" - the transport
// form with the highest survival share is the region's recommended transport; when it differs from the region's
// current mainstream transport, a switch suggestion is given. Switching = changing configuration, not architecture (change the node's transport annotation and re-push the corresponding config, reusing the config.push pipeline).
package transport

import (
	"sort"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// DefaultWindow is the default window value, same policy as the regional aggregate judgment.
const DefaultWindow = 5 * time.Minute

// Options is the judgment policy; the zero value falls back to the default.
type Options struct {
	Window time.Duration
}

func (o Options) normalize() Options {
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	return o
}

// TransportHealth is a transport's survival summary within a region.
type TransportHealth struct {
	Transport string `json:"transport"`
	Total     int    `json:"total"` // nodes with fresh conclusions within the window
	Alive     int    `json:"alive"` // nodes with a fresh healthy conclusion
}

// Row is one region's transport judgment conclusion.
type Row struct {
	Region      string            `json:"region"`
	Transports  []TransportHealth `json:"transports"`
	Current     string            `json:"current"`      // regional current mainstream transport (the most nodes)
	Recommended string            `json:"recommended"`  // the transport with the highest survival share
	Switch      bool              `json:"switchneeded"` // recommended != current, suggests switching lines
}

// Judge aggregates the window's mutual-probe / tunnel conclusions, judging per region x transport (E-17).
// Each target node takes its latest conclusion to record liveness; nodes with no fresh conclusion do not participate.
func Judge(db *gorm.DB, opts Options) ([]Row, error) {
	opts = opts.normalize()
	since := time.Now().Add(-opts.Window)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at >= ? AND target_kind IN ?",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return nil, err
	}
	// Take the latest conclusion for each target node.
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
	// The target node's region and transport annotations.
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
			// Current mainstream: the most nodes (ties broken by transport name in lexicographic order, for stability).
			if cur := findHealth(healths, row.Current); cur == nil || h.Total > cur.Total {
				row.Current = h.Transport
			}
			// Recommended: the highest survival share (compare alive counts at the same share, then lexicographic order).
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

// better compares whether a is more worth recommending than b: survival share first, then alive count.
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
