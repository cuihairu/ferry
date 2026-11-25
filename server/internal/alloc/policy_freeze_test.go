package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/policy"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSweepFailoverFreeze covers F-0294: a system policy with failover=manual
// stops the auto landing allocation sweep; auto (or no policy) keeps sweeping.
func TestSweepFailoverFreeze(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "frz-entry", "entry", "out", nil)
	seedNode(t, db, "frz-land", "landing", "out", nil)

	seedPolicy := func(failover string) {
		// One policy per scope+subject: replace the system row per case.
		if err := db.Where("scope = ?", storage.PolicyScopeSystem).
			Delete(&storage.Policy{}).Error; err != nil {
			t.Fatal(err)
		}
		c := storage.PolicyConstraints{}
		if failover != "" {
			c.Failover = failover
		}
		if err := db.Create(&storage.Policy{
			Scope: storage.PolicyScopeSystem, SubjectID: 0, Enabled: true,
			Constraints: storage.MustPolicyConstraints(c), Version: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// failover=manual: the sweep is a no-op, no assignment is created.
	seedPolicy("manual")
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("frozen sweep events = %+v", events)
	}
	var rows int64
	db.Model(&storage.LandingAssignment{}).Count(&rows)
	if rows != 0 {
		t.Fatalf("frozen sweep wrote assignments: %d", rows)
	}

	// failover=auto: the sweep allocates normally.
	seedPolicy("auto")
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "frz-land" {
		t.Fatalf("auto sweep events = %+v", events)
	}

	// No policy row: sweep still allocates (zero policies, zero change).
	if err := db.Where("scope = ?", storage.PolicyScopeSystem).
		Delete(&storage.Policy{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := policy.Resolve(db, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("1 = 1").Delete(&storage.LandingAssignment{}).Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "frz-land" {
		t.Fatalf("no-policy sweep events = %+v", events)
	}
}
