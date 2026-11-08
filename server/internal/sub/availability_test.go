package sub

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUserActive 覆盖启用/到期/超配额/不限配额四态。
func TestUserActive(t *testing.T) {
	now := time.Now()
	u := &storage.User{Enabled: true}
	if !UserActive(u, 0, now) {
		t.Fatal("default active")
	}
	// 停用。
	u.Enabled = false
	if UserActive(u, 0, now) {
		t.Fatal("disabled should be inactive")
	}
	// 到期。
	u.Enabled = true
	u.ExpiresAt = &[]time.Time{now.Add(-time.Hour)}[0]
	if UserActive(u, 0, now) {
		t.Fatal("expired should be inactive")
	}
	// 未到期但超配额。
	u.ExpiresAt = &[]time.Time{now.Add(time.Hour)}[0]
	u.QuotaBytes = 100
	if UserActive(u, 100, now) {
		t.Fatal("quota exhausted should be inactive")
	}
	// 未超配额。
	if !UserActive(u, 99, now) {
		t.Fatal("under quota should be active")
	}
	// 配额 0 = 不限。
	u.QuotaBytes = 0
	if !UserActive(u, 1<<40, now) {
		t.Fatal("unlimited quota should be active")
	}
}
