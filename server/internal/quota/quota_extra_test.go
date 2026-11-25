package quota

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestUsedBytes covers UsedBytes' no-window/window/scan-error branches.
func TestUsedBytes(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s, _ := db.DB(); s != nil {
			_ = s.Close()
		}
	})
	now := time.Now()
	// Create a user to satisfy the foreign key constraint.
	u := storage.User{Username: "quota-used"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// No traffic: returns 0.
	if got, err := UsedBytes(db, u.ID, CycleNone, now); err != nil || got != 0 {
		t.Fatalf("empty: %d %v", got, err)
	}
	// Insert one traffic row (no-window cycle).
	uid := u.ID
	if err := db.Create(&storage.TrafficLog{UserID: &uid, RxBytes: 30, TxBytes: 20, RecordedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := UsedBytes(db, u.ID, CycleNone, now); err != nil || got != 50 {
		t.Fatalf("no window: %d %v", got, err)
	}
	// Windowed (day) hit.
	if got, err := UsedBytes(db, u.ID, CycleDay, now); err != nil || got != 50 {
		t.Fatalf("day window: %d %v", got, err)
	}
	// Scan error: close the DB.
	s, _ := db.DB()
	_ = s.Close()
	if _, err := UsedBytes(db, u.ID, CycleNone, now); err == nil {
		t.Fatal("want error on closed db")
	}
}
