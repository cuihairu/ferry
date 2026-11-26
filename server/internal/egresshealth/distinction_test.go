package egresshealth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/nodehealth"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestEgressDistinctFromNodeHealth pins the F-0905 distinction: the exit
// baseline exists to tell "others cannot reach this node" (node health,
// tunnel/peer verdicts about it) apart from "this node cannot reach the
// outside" (egress health, its own exit verdicts). A node whose peers see it
// healthy but whose own exit probe reports sick is healthy as a node and
// degraded as an egress -- and neither derivation leaks into the other.
func TestEgressDistinctFromNodeHealth(t *testing.T) {
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "egdist.db"))
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
	n := storage.Node{Name: "eg-dist", Token: "tok-eg-dist", Status: "online",
		PoolState: "active", Enabled: true}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// Peers see the node healthy (fresh peer verdict about it).
	peer := storage.Node{Name: "eg-prober", Token: "tok-eg-prober"}
	if err := db.Create(&peer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.ProbeReport{
		NodeID: peer.ID, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &n.ID,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	// Before any exit report: node healthy, egress unknown (different
	// derivations, different absence semantics).
	nh := nodehealth.Evaluate(db, []storage.Node{n}, now)
	eh := Evaluate(db, []storage.Node{n}, now)
	if nh[n.ID] != nodehealth.Healthy || eh[n.ID] != Unknown {
		t.Fatalf("baseline: node=%q egress=%q", nh[n.ID], eh[n.ID])
	}

	// The node's own exit probe reports sick: egress degrades, the node's
	// peer-perceived health does not.
	seedExit(t, db, n.ID, agentproto.ProbeVerdictSick, time.Minute)
	nh = nodehealth.Evaluate(db, []storage.Node{n}, now)
	eh = Evaluate(db, []storage.Node{n}, now)
	if eh[n.ID] != Degraded {
		t.Fatalf("own sick exit must degrade egress = %q", eh[n.ID])
	}
	if nh[n.ID] != nodehealth.Healthy {
		t.Fatalf("own exit verdict must not degrade node health = %q", nh[n.ID])
	}
}
