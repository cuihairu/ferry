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
