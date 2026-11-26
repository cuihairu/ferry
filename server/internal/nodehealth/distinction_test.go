package nodehealth

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeAndPathHealthIndependent pins the F-0904 distinction: node health
// and path health are separate derivations over the same signals. A node
// going off takes down nodehealth for that node and degrades only the paths
// through it, never the other paths; and per-path states are recomputed by
// pathhealth's own rules, not inherited from the node state map.
func TestNodeAndPathHealthIndependent(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "dist.db"))
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
	mk := func(name string) storage.Node {
		t.Helper()
		n := storage.Node{Name: name, Token: "tok-" + name, Status: "online",
			PoolState: "active", Enabled: true}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	entryA, landingA := mk("d-entry-a"), mk("d-land-a")
	entryB, landingB := mk("d-entry-b"), mk("d-land-b")
	mkPath := func(entry, landing storage.Node, tag string) storage.Path {
		t.Helper()
		p := storage.Path{Name: fmt.Sprintf("d-path-%s", tag), EntryNodeID: entry.ID,
			LandingNodeID: landing.ID, Enabled: true}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	pathA := mkPath(entryA, landingA, "a")
	pathB := mkPath(entryB, landingB, "b")

	now := time.Now()
	// Baseline: no verdicts anywhere -- every dimension is unknown.
	nh := Evaluate(db, []storage.Node{entryA, landingA, entryB, landingB}, now)
	ph := pathhealth.Evaluate(db, []storage.Path{pathA, pathB}, now)
	if nh[entryB.ID] != Unknown || ph[pathB.ID] != Unknown {
		t.Fatalf("baseline: node=%q path=%q", nh[entryB.ID], ph[pathB.ID])
	}

	// entryA goes off: its own health is off, path A degrades through it, and
	// path B plus the other nodes are untouched.
	entryA.Status = "offline"
	if err := db.Model(&storage.Node{}).Where("id = ?", entryA.ID).
		UpdateColumn("status", "offline").Error; err != nil {
		t.Fatal(err)
	}
	nh = Evaluate(db, []storage.Node{entryA, landingA, entryB, landingB}, now)
	ph = pathhealth.Evaluate(db, []storage.Path{pathA, pathB}, now)
	if nh[entryA.ID] != Off {
		t.Fatalf("entryA node health = %q", nh[entryA.ID])
	}
	if ph[pathA.ID] != pathhealth.Degraded {
		t.Fatalf("path A through offline entry = %q", ph[pathA.ID])
	}
	if ph[pathB.ID] != Unknown || nh[entryB.ID] != Unknown {
		t.Fatalf("path B must be untouched: node=%q path=%q", nh[entryB.ID], ph[pathB.ID])
	}

	// A fresh healthy verdict about landingB: path B flips to healthy and
	// landingB's own health follows its own rule set -- independently of path A.
	seedVerdict(t, db, entryB.ID, landingB.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	nh = Evaluate(db, []storage.Node{landingB, landingA}, now)
	ph = pathhealth.Evaluate(db, []storage.Path{pathA, pathB}, now)
	if ph[pathB.ID] != pathhealth.Healthy || nh[landingB.ID] != Healthy {
		t.Fatalf("healthy verdict: path=%q node=%q", ph[pathB.ID], nh[landingB.ID])
	}
	if ph[pathA.ID] != pathhealth.Degraded {
		t.Fatalf("path A must stay degraded: %q", ph[pathA.ID])
	}
}
