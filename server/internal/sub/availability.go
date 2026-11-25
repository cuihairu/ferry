package sub

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// UserActive decides whether a subscription user may still receive nodes (P0-8): enabled, not expired, within quota.
// quota==0 means unlimited. Expired/over-quota users get an empty subscription (with usage headers) and the client shows the status.
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
