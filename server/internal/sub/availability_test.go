package sub

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserActive covers the four states: enabled / expired / over quota / unlimited quota.
func TestUserActive(t *testing.T) {
	now := time.Now()
	u := &storage.User{Enabled: true}
	if !UserActive(u, 0, now) {
		t.Fatal("default active")
	}
	// Disabled.
	u.Enabled = false
	if UserActive(u, 0, now) {
		t.Fatal("disabled should be inactive")
	}
	// Expired.
	u.Enabled = true
	u.ExpiresAt = &[]time.Time{now.Add(-time.Hour)}[0]
	if UserActive(u, 0, now) {
		t.Fatal("expired should be inactive")
	}
	// Not expired but over quota.
	u.ExpiresAt = &[]time.Time{now.Add(time.Hour)}[0]
	u.QuotaBytes = 100
	if UserActive(u, 100, now) {
		t.Fatal("quota exhausted should be inactive")
	}
	// Within quota.
	if !UserActive(u, 99, now) {
		t.Fatal("under quota should be active")
	}
	// Quota 0 = unlimited.
	u.QuotaBytes = 0
	if !UserActive(u, 1<<40, now) {
		t.Fatal("unlimited quota should be active")
	}
}

// TestUserStateOf covers the split gate state (G3): the expiry judgment and the
// quota judgment are independent, and Status carries the primary cause. The
// three required paths are expired-not-limited, limited-not-expired, and both.
func TestUserStateOf(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-time.Hour)
	futureAt := now.Add(time.Hour)

	cases := []struct {
		name       string
		enabled    bool
		expiresAt  *time.Time
		quotaBytes int64
		used       int64
		want       UserState
	}{
		{
			name:       "active",
			enabled:    true,
			expiresAt:  &futureAt,
			quotaBytes: 100,
			used:       10,
			want:       UserState{Status: StatusActive},
		},
		{
			name:       "expired but within quota",
			enabled:    true,
			expiresAt:  &expiredAt,
			quotaBytes: 100,
			used:       10,
			want:       UserState{Status: StatusExpired, Expired: true},
		},
		{
			name:       "over quota but not expired",
			enabled:    true,
			expiresAt:  &futureAt,
			quotaBytes: 100,
			used:       100,
			want:       UserState{Status: StatusLimited, Limited: true},
		},
		{
			name:       "expired and over quota",
			enabled:    true,
			expiresAt:  &expiredAt,
			quotaBytes: 100,
			used:       100,
			// Both flags stay true; Status names the primary cause (expiry first).
			want: UserState{Status: StatusExpired, Expired: true, Limited: true},
		},
		{
			name:       "never expires and unlimited quota",
			enabled:    true,
			quotaBytes: 0,
			used:       1 << 40,
			want:       UserState{Status: StatusActive},
		},
		{
			name:       "disabled wins over expiry and quota",
			enabled:    false,
			expiresAt:  &expiredAt,
			quotaBytes: 100,
			used:       100,
			want:       UserState{Status: StatusDisabled},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UserStateOf(&storage.User{Enabled: c.enabled, ExpiresAt: c.expiresAt, QuotaBytes: c.quotaBytes}, c.used, now)
			if got != c.want {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			if UserActive(&storage.User{Enabled: c.enabled, ExpiresAt: c.expiresAt, QuotaBytes: c.quotaBytes}, c.used, now) != (c.want.Status == StatusActive) {
				t.Fatalf("UserActive disagrees with state %+v", got)
			}
		})
	}
}
