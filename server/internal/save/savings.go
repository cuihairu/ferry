package save

import "gorm.io/gorm"

// UserSavings converts a user's "saved for you" for the current month (shared by the SAVE-8 panel report and
// the TOUCH-4 monthly billing highlight): the node-level savings counters (save_stats) come from the agent's
// outbound counting and carry no user identity, so they are prorated by the user's share of the billed traffic on
// that node this month (a reviewable estimation formula); when a node has no billed user traffic for the month it is not apportioned. The window runs from monthStart (YYYY-MM-01 format) to the end of the current calendar month (UTC).
func UserSavings(db *gorm.DB, userID uint, monthStart string) (direct, cacheHit, blocked int64, err error) {
	var saves []struct {
		NodeID   uint
		Direct   int64
		CacheHit int64
		Blocked  int64
	}
	if err = db.Table("save_stats").
		Select("node_id, COALESCE(SUM(direct_bytes),0) AS direct, COALESCE(SUM(cache_hit_bytes),0) AS cache_hit, COALESCE(SUM(blocked_bytes),0) AS blocked").
		Where("day >= ?", monthStart).
		Group("node_id").Scan(&saves).Error; err != nil {
		return
	}
	var usages []struct {
		NodeID uint
		Total  int64
		Mine   int64
	}
	if err = db.Table("traffic_logs").
		Select("node_id, COALESCE(SUM(rx_bytes+tx_bytes),0) AS total, COALESCE(SUM(CASE WHEN user_id = ? THEN rx_bytes+tx_bytes ELSE 0 END),0) AS mine", userID).
		Where("recorded_at >= ? AND node_id IS NOT NULL", monthStart).
		Group("node_id").Scan(&usages).Error; err != nil {
		return
	}
	byNode := make(map[uint]struct{ total, mine int64 }, len(usages))
	for _, us := range usages {
		byNode[us.NodeID] = struct{ total, mine int64 }{us.Total, us.Mine}
	}
	for _, s := range saves {
		us, ok := byNode[s.NodeID]
		if !ok || us.total <= 0 {
			continue
		}
		share := float64(us.mine) / float64(us.total)
		direct += int64(float64(s.Direct) * share)
		cacheHit += int64(float64(s.CacheHit) * share)
		blocked += int64(float64(s.Blocked) * share)
	}
	return
}
