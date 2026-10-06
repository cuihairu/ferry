package sub

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// UserActive 判断订阅用户是否可出节点（P0-8）：启用、未到期、未超配额。
// quota==0 表示不限。到期/超限用户返回空订阅（带用量头），由客户端展示状态。
func UserActive(u *storage.User, usedBytes int64, now time.Time) bool {
	if !u.Enabled {
		return false
	}
	if u.ExpiresAt != nil && now.After(*u.ExpiresAt) {
		return false
	}
	if u.QuotaBytes > 0 && usedBytes >= u.QuotaBytes {
		return false
	}
	return true
}
