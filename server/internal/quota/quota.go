// Package quota traffic reset cycle model (P1-4, aligned with Marzban data_limit_reset_strategy):
// none = cumulative total; day/week/month = calendar-window usage only, a rolling window acts as the "reset".
// Accounting details (traffic_logs) are never deleted or modified; resetting only shows up as window filtering in evaluation and display.
package quota

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Supported reset cycles.
const (
	CycleNone  = "none"
	CycleDay   = "day"
	CycleWeek  = "week"
	CycleMonth = "month"
)

// ValidCycle reports whether the cycle value is one of none/day/week/month.
func ValidCycle(c string) bool {
	switch c {
	case CycleNone, CycleDay, CycleWeek, CycleMonth:
		return true
	}
	return false
}

// WindowStart returns the start of the current billing window (inclusive); none or an unknown cycle
// returns the zero value meaning cumulative total. Windows follow local server timezone calendar boundaries: day = midnight today,
// week = Monday 00:00 this week, month = the 1st of this month 00:00.
func WindowStart(cycle string, now time.Time) time.Time {
	loc := now.Location()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch cycle {
	case CycleDay:
		return dayStart
	case CycleWeek:
		// Go weekday: Sunday=0; Monday is the boundary and Sunday belongs to this week (step back 6 days).
		wd := int(dayStart.Weekday())
		if wd == 0 {
			wd = 7
		}
		return dayStart.AddDate(0, 0, -(wd - 1))
	case CycleMonth:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	default:
		return time.Time{}
	}
}

// UsedBytes returns the user's bytes used within the current billing window (rx+tx combined).
func UsedBytes(db *gorm.DB, userID uint, cycle string, now time.Time) (int64, error) {
	q := db.Model(&storage.TrafficLog{}).
		Select("COALESCE(SUM(rx_bytes + tx_bytes), 0)").
		Where("user_id = ?", userID)
	if since := WindowStart(cycle, now); !since.IsZero() {
		q = q.Where("recorded_at >= ?", since)
	}
	var used int64
	if err := q.Scan(&used).Error; err != nil {
		return 0, err
	}
	return used, nil
}
