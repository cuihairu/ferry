package alloc

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestHasManualAssignmentExpiry covers the TTL gate on manual effectiveness
// (F-1050): a manual row is effective until its expires_at, then it no longer
// blocks auto.
func TestHasManualAssignmentExpiry(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "ttl-entry", "entry", "out", nil)
	landing := seedNode(t, db, "ttl-land", "landing", "out", nil)

	mk := func(expires *time.Time) {
		// one row at a time: the earlier phase's row must not leak into the
		// next count
		if err := db.Exec("DELETE FROM landing_assignments").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&storage.LandingAssignment{
			EntryNodeID: &entry.ID, LandingNodeID: landing.ID,
			Direction: "out", Strategy: PolicyManual, AssignedAt: testNow, ExpiresAt: expires,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk(nil)
	if ok, err := hasManualAssignment(db, entry.ID, "", testNow); err != nil || !ok {
		t.Fatalf("no TTL must be effective: ok=%v err=%v", ok, err)
	}
	future := testNow.Add(time.Hour)
	mk(&future)
	if ok, err := hasManualAssignment(db, entry.ID, "", testNow); err != nil || !ok {
		t.Fatalf("future TTL must be effective: ok=%v err=%v", ok, err)
	}
	past := testNow.Add(-time.Hour)
	mk(&past)
	if ok, err := hasManualAssignment(db, entry.ID, "", testNow); err != nil || ok {
		t.Fatalf("past TTL must not be effective: ok=%v err=%v", ok, err)
	}
}

// TestSweepManualTTL covers the sweep recovering an expired manual override
// (F-1050): the row is released with the manual_expired trail and the entry
// is auto-assigned in the same round; a live TTL keeps the override intact.
func TestSweepManualTTL(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "mt-entry", "entry", "out", nil)
	landing := seedNode(t, db, "mt-land", "landing", "out", nil)

	// a live override holds: no release, no auto events
	expires := testNow.Add(time.Hour)
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landing.ID,
		Direction: "out", Strategy: PolicyManual, AssignedAt: testNow, ExpiresAt: &expires,
	}).Error; err != nil {
		t.Fatal(err)
	}
	events, err := SweepAt(db, testNow)
	if err != nil || len(events) != 0 {
		t.Fatalf("live TTL must hold: %+v err=%v", events, err)
	}

	// once the TTL passes, the sweep releases the row and auto-assigns
	past := testNow.Add(-time.Hour)
	if err := db.Model(&storage.LandingAssignment{}).Where("id > 0").
		Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	events, err = SweepAt(db, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EntryName != entry.Name || events[0].ToName != landing.Name {
		t.Fatalf("expired override must hand back to auto: %+v", events)
	}
	var rows []storage.LandingAssignment
	if err := db.Where("strategy = ?", PolicyManual).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ReleasedAt == nil || rows[0].ReleaseReason != ManualExpiredReason {
		t.Fatalf("manual row trail = %+v", rows)
	}
}
