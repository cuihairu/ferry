package pathhealth

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newTestDB opens an isolated migrated database (the alloc package's helper
// lives in another package tree; a small local copy keeps this package
// self-contained).
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func mkNode(t *testing.T, db *gorm.DB, name, status, pool string) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Status: status, PoolState: pool}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// seedVerdict records one probe verdict against targetNode at offset back from now.
func seedVerdict(t *testing.T, db *gorm.DB, prober uint, target uint, verdict string, offset time.Duration) {
	t.Helper()
	if err := db.Create(&storage.ProbeReport{
		NodeID: prober, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: verdict, ProbedAt: time.Now().Add(-offset),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func mkPath(t *testing.T, db *gorm.DB, entry, landing uint) storage.Path {
	t.Helper()
	p := storage.Path{Name: fmt.Sprintf("p-%d", landing), EntryNodeID: entry, LandingNodeID: landing}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// TestEvaluateStates covers F-0251's state rules: off/degraded/healthy/unknown.
func TestEvaluateStates(t *testing.T) {
	db := newTestDB(t)
	entry := mkNode(t, db, "entry", "online", "active")
	landing := mkNode(t, db, "landing", "online", "active")
	path := mkPath(t, db, entry.ID, landing.ID)

	now := time.Now()

	// No probe data at all: unknown (endpoints up, but no fresh verdict).
	got := Evaluate(db, []storage.Path{path}, now)
	if got[path.ID] != Unknown {
		t.Fatalf("no verdict = %q", got[path.ID])
	}

	// Fresh healthy probe: healthy.
	seedVerdict(t, db, entry.ID, landing.ID, agentproto.ProbeVerdictHealthy, 5*time.Minute)
	got = Evaluate(db, []storage.Path{path}, now)
	if got[path.ID] != Healthy {
		t.Fatalf("healthy verdict = %q", got[path.ID])
	}

	// Fresh sick probe: degraded.
	seedVerdict(t, db, entry.ID, landing.ID, agentproto.ProbeVerdictSick, 5*time.Minute)
	got = Evaluate(db, []storage.Path{path}, now)
	if got[path.ID] != Degraded {
		t.Fatalf("sick verdict = %q", got[path.ID])
	}

	// Stale probe only (older than the window): no fresh verdict -> unknown.
	staleLand := mkNode(t, db, "stale-land", "online", "active")
	stalePath := mkPath(t, db, entry.ID, staleLand.ID)
	seedVerdict(t, db, entry.ID, staleLand.ID, agentproto.ProbeVerdictHealthy, ProbeFresh+time.Minute)
	got = Evaluate(db, []storage.Path{stalePath}, now)
	if got[stalePath.ID] != Unknown {
		t.Fatalf("stale verdict = %q", got[stalePath.ID])
	}

	// Offline endpoint: degraded even with a healthy probe.
	seedVerdict(t, db, entry.ID, landing.ID, agentproto.ProbeVerdictHealthy, 5*time.Minute)
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	got = Evaluate(db, []storage.Path{path}, now)
	if got[path.ID] != Degraded {
		t.Fatalf("offline entry = %q", got[path.ID])
	}

	// Pool-suspended landing: degraded too.
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("status", "online").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", landing.ID).
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	got = Evaluate(db, []storage.Path{path}, now)
	if got[path.ID] != Degraded {
		t.Fatalf("suspended landing = %q", got[path.ID])
	}

	// Disabled path: off regardless (reloaded so the flag is seen).
	if err := db.Model(&storage.Node{}).Where("id = ?", landing.ID).
		Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Path{}).Where("id = ?", path.ID).
		Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	var disabled storage.Path
	if err := db.First(&disabled, path.ID).Error; err != nil {
		t.Fatal(err)
	}
	got = Evaluate(db, []storage.Path{disabled}, now)
	if got[disabled.ID] != Off {
		t.Fatalf("disabled path = %q", got[disabled.ID])
	}
}

// TestGroupHealthAndAvailability covers F-0268 (worst-of aggregation over
// non-excluded members) and F-0269 (healthy share over the stats window).
func TestGroupHealthAndAvailability(t *testing.T) {
	db := newTestDB(t)
	entry := mkNode(t, db, "entry", "online", "active")
	landA := mkNode(t, db, "land-a", "online", "active")
	landB := mkNode(t, db, "land-b", "offline", "active")
	pathA := mkPath(t, db, entry.ID, landA.ID)
	pathB := mkPath(t, db, entry.ID, landB.ID)

	if err := db.Create(&storage.PathGroup{Name: "g1", Direction: "out"}).Error; err != nil {
		t.Fatal(err)
	}
	var group storage.PathGroup
	if err := db.Where("name = ?", "g1").First(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.PathGroupMember{GroupID: group.ID, PathID: pathA.ID}).Error; err != nil {
		t.Fatal(err)
	}
	// B is excluded: it must not drag the aggregate down.
	if err := db.Create(&storage.PathGroupMember{GroupID: group.ID, PathID: pathB.ID, Excluded: true}).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	seedVerdict(t, db, entry.ID, landA.ID, agentproto.ProbeVerdictHealthy, time.Minute)

	state, members, err := GroupHealth(db, group.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if state != Healthy {
		t.Fatalf("aggregate with excluded sick member = %q", state)
	}
	if len(members) != 1 || members[0].PathID != pathA.ID || members[0].Health != Healthy {
		t.Fatalf("members = %+v", members)
	}

	// A sick verdict on the active member degrades the group.
	seedVerdict(t, db, entry.ID, landA.ID, agentproto.ProbeVerdictSick, time.Minute)
	state, _, err = GroupHealth(db, group.ID, now)
	if err != nil || state != Degraded {
		t.Fatalf("sick member aggregate = %q err=%v", state, err)
	}

	// Availability: 1 healthy + 1 sick over the window = 50.
	pct, err := Availability(db, group.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if pct == nil || *pct != 50 {
		t.Fatalf("availability = %v", pct)
	}

	// No probe data at all: null.
	if err := db.Where("1 = 1").Delete(&storage.ProbeReport{}).Error; err != nil {
		t.Fatal(err)
	}
	pct, err = Availability(db, group.ID, now)
	if err != nil || pct != nil {
		t.Fatalf("no-data availability = %v err=%v", pct, err)
	}
}
