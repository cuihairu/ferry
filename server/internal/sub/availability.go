package sub

import (
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// User status values (G3): one shared vocabulary for the subscription surface,
// the admin views and the bot/panel APIs. A user can be expired and limited at
// the same time; Status collapses that into the primary cause by the precedence
// disabled > expired > limited > active, while the Expired/Limited flags keep
// the two independent judgments so support can tell the sources apart.
const (
	StatusActive   = "active"
	StatusExpired  = "expired"
	StatusLimited  = "limited"
	StatusDisabled = "disabled"
)

// UserState is the split view of the two independent gate conditions (G3).
// Expired is judged from ExpiresAt alone, Limited from a positive QuotaBytes
// against usedBytes alone; Status is the display-ready primary cause (quota==0
// is unlimited and can never be limited).
type UserState struct {
	Status  string
	Expired bool
	Limited bool
}

// UserStateOf judges the two gate conditions independently and derives Status.
func UserStateOf(u *storage.User, usedBytes int64, now time.Time) UserState {
	st := UserState{Status: StatusActive}
	if !u.Enabled {
		st.Status = StatusDisabled
		return st
	}
	if u.ExpiresAt != nil && now.After(*u.ExpiresAt) {
		st.Expired = true
		st.Status = StatusExpired
	}
	if u.QuotaBytes > 0 && usedBytes >= u.QuotaBytes {
		st.Limited = true
		if !st.Expired {
			st.Status = StatusLimited
		}
	}
	return st
}

// UserActive decides whether a subscription user may still receive nodes (P0-8): enabled, not expired, within quota.
// quota==0 means unlimited. Expired/over-quota users get an empty subscription (with usage headers) and the client shows the status.
func UserActive(u *storage.User, usedBytes int64, now time.Time) bool {
	return UserStateOf(u, usedBytes, now).Status == StatusActive
}
