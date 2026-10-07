// Package quota 流量重置周期口径（P1-4，对齐 Marzban data_limit_reset_strategy）：
// none=全量累计，day/week/month=按日历窗口只算窗口内用量，窗口滚动即"重置"。
// 记账明细（traffic_logs）不删不改，重置只体现在判定与展示的窗口过滤上。
package quota

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 支持的重置周期。
const (
	CycleNone  = "none"
	CycleDay   = "day"
	CycleWeek  = "week"
	CycleMonth = "month"
)

// ValidCycle 判断周期取值是否在 none/day/week/month 内。
func ValidCycle(c string) bool {
	switch c {
	case CycleNone, CycleDay, CycleWeek, CycleMonth:
		return true
	}
	return false
}

// WindowStart 返回当前计费窗口的起点（含该时刻）；none 或未知周期返回
// 零值表示全量累计。窗口按服务器本地时区的日历边界：day=当日 00:00，
// week=本周一 00:00，month=本月 1 日 00:00。
func WindowStart(cycle string, now time.Time) time.Time {
	loc := now.Location()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch cycle {
	case CycleDay:
		return dayStart
	case CycleWeek:
		// Go weekday：Sunday=0；周一为界，周日归本周（回退 6 天）。
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

// UsedBytes 返回用户在当前计费窗口内的已用字节（rx+tx 合计）。
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
