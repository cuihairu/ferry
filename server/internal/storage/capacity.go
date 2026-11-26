package storage

import (
	"time"

	"gorm.io/gorm"
)

// LoadFresh is the traffic-sample freshness window: older samples count as
// absent for every load consumer.
const LoadFresh = 30 * time.Minute

// CapacityMbps is the node's forwarding capacity (Mbps), one fallback chain
// for every consumer (F-1061/1062/1063): the speed calibration when present,
// then the plan downlink, then the plan uplink, and a 100 Mbps floor when
// nothing is known. Bandwidth doubles as the scheduling capacity weight --
// a 3M small pipe receives fewer new connections by capacity, so it never
// floods over.
func (n Node) CapacityMbps() int {
	switch {
	case n.SpeedMeasuredMbps > 0:
		return n.SpeedMeasuredMbps
	case n.BwDownMbps > 0:
		return n.BwDownMbps
	case n.BwUpMbps > 0:
		return n.BwUpMbps
	}
	return 100
}

// UtilizationPct folds measured throughput into a percent of capacity, one
// formula for every consumer of the load view (F-1064). Without an observed
// sample (active false) the utilization is unknown and reported as -1
// rather than a misleading 0%.
func UtilizationPct(mbps float64, capacityMbps int, active bool) int {
	if !active {
		return -1
	}
	return int(mbps / float64(capacityMbps) * 100)
}

// LoadSample is one node's load snapshot: the connection count summed over
// the latest sample of each proc, and the throughput diffed from the latest
// sample pair.
type LoadSample struct {
	Conns int
	Mbps  float64
}

// LatestLoad folds fresh traffic samples into per-node load (F-1064): one
// sampling path shared by the panel load view and the overload monitor. The
// connection count takes each proc's latest sample; throughput diffs each
// proc's newest pair of samples (a pair closer than 10s, or a single
// sample, is too coarse to diff and counts only toward conns).
func LatestLoad(db *gorm.DB, since time.Time) (map[uint]LoadSample, error) {
	var logs []NodeTrafficLog
	if err := db.Where("recorded_at > ?", since).
		Order("recorded_at ASC, id ASC").Find(&logs).Error; err != nil {
		return nil, err
	}
	type sample struct {
		rx, tx, conns int64
		at            time.Time
	}
	latest := map[uint]map[string][2]sample{} // node -> proc -> two latest
	for _, l := range logs {
		if latest[l.NodeID] == nil {
			latest[l.NodeID] = map[string][2]sample{}
		}
		pair := latest[l.NodeID][l.Proc]
		pair[0], pair[1] = pair[1], sample{int64(l.RxBytes), int64(l.TxBytes), int64(l.Conns), l.RecordedAt}
		latest[l.NodeID][l.Proc] = pair
	}
	out := make(map[uint]LoadSample, len(latest))
	for id, procs := range latest {
		var s LoadSample
		for _, pair := range procs {
			s.Conns += int(pair[1].conns)
			dt := pair[1].at.Sub(pair[0].at).Seconds()
			if dt < 10 || pair[0].at.IsZero() {
				continue
			}
			s.Mbps += float64(pair[1].rx-pair[0].rx+pair[1].tx-pair[0].tx) * 8 / dt / 1e6
		}
		out[id] = s
	}
	return out, nil
}
