package alloc

import (
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Path quality scoring (F-1028): the probe telemetry a candidate already
// produces (tunnel/peer verdicts carry RTT and loss) folded into one
// comparable score per landing. The score is a primitive the output model and
// the preview render; the policies keep their deterministic rules (F-1046),
// and a quality-first policy consumes it in its own row (F-1044).

// Quality is a candidate landing's fresh probe quality, averaged across the
// probers that reported it. Samples 0 = no fresh report (unknown).
type Quality struct {
	RttMs   int // mean RTT of the fresh reports
	LossPct int // mean loss percentage of the fresh reports
	Samples int // how many probers reported within the window
}

// LatestQuality aggregates each target node's fresh probe quality: within the
// health window, the latest report per prober survives, and the survivors are
// averaged per target. A node nobody freshly probed is absent from the map --
// absent means unknown, which must not read as bad data.
func LatestQuality(db *gorm.DB) map[uint]Quality {
	out := map[uint]Quality{}
	since := time.Now().Add(-ProbeFresh)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at > ? AND target_kind IN ? AND target_node_id IS NOT NULL",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return out // on query failure treat as no sample, without blocking allocation
	}
	// Last write wins per (target, prober) = the prober's latest verdict.
	latest := map[uint]map[uint]storage.ProbeReport{}
	for _, r := range reports {
		target := *r.TargetNodeID
		if latest[target] == nil {
			latest[target] = map[uint]storage.ProbeReport{}
		}
		latest[target][r.NodeID] = r
	}
	for target, probers := range latest {
		q := Quality{}
		for _, r := range probers {
			q.RttMs += r.RttMs
			q.LossPct += r.LossPct
			q.Samples++
		}
		q.RttMs /= q.Samples
		q.LossPct /= q.Samples
		out[target] = q
	}
	return out
}

// Quality score caps: RTT beyond half a second and loss at 100% both floor
// their term, so a catastrophically bad line cannot score below the floor.
const (
	qualityRttCapMs  = 500
	qualityLossCap   = 100
	qualityLossShare = 0.7 // loss dominates: a lossy line is unusable before it is slow
)

// QualityScore folds a candidate's probe quality into a [0,1] score: loss
// weighted 0.7, RTT 0.3. A candidate with no fresh sample scores the neutral
// 0.5 -- the same constant the score normalizer gives all-equal metrics -- so
// cold starts are neither penalized nor favored over freshly scored lines.
func QualityScore(q Quality) float64 {
	if q.Samples == 0 {
		return 0.5
	}
	loss := min(q.LossPct, qualityLossCap)
	rtt := min(q.RttMs, qualityRttCapMs)
	return qualityLossShare*(1-float64(loss)/qualityLossCap) +
		(1-qualityLossShare)*(1-float64(rtt)/qualityRttCapMs)
}
