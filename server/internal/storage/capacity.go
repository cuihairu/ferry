package storage

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
