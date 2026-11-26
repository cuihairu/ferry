package egresshealth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "egress.db"))
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

func mkNode(t *testing.T, db *gorm.DB, name, status, pool string, enabled bool) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Status: status, PoolState: pool, Enabled: enabled}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// seedExit records one exit baseline verdict produced by prober at offset
// back from now (exit targets an external baseline host, so no target node).
func seedExit(t *testing.T, db *gorm.DB, prober uint, verdict string, offset time.Duration) {
	t.Helper()
	if err := db.Create(&storage.ProbeReport{
		NodeID: prober, TargetKind: agentproto.ProbeTargetExit,
		TargetHost: "baseline.example", Verdict: verdict,
		ProbedAt: time.Now().Add(-offset),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// TestEvaluateRules covers F-0905's decision table: the node's own exit
// baseline decides; disabled/offline is off; suspension does not enter this
// derivation; absence of verdicts is unknown -- never invented health.
func TestEvaluateRules(t *testing.T) {
	db := newTestDB(t)
	live := mkNode(t, db, "eg-live", "online", "active", true)
	susp := mkNode(t, db, "eg-susp", "online", "suspended", true)
	off := mkNode(t, db, "eg-off", "offline", "active", true)
	dis := mkNode(t, db, "eg-dis", "online", "active", false)

	// gorm's default:true swallows a false Enabled on Create and the sqlite
	// RETURNING clause even refills the struct; pin DB and struct explicitly.
	if err := db.Model(&storage.Node{}).Where("id = ?", dis.ID).
		UpdateColumn("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	dis.Enabled = false

	now := time.Now()
	got := Evaluate(db, []storage.Node{live, susp, off, dis}, now)
	if got[live.ID] != Unknown {
		t.Fatalf("live without exit verdicts = %q (absence is not health)", got[live.ID])
	}
	if got[susp.ID] != Unknown {
		t.Fatalf("suspended egress must stay verdict-driven = %q", got[susp.ID])
	}
	if got[off.ID] != Off {
		t.Fatalf("offline egress = %q", got[off.ID])
	}
	if got[dis.ID] != Off {
		t.Fatalf("disabled egress = %q", got[dis.ID])
	}

	// A fresh sick exit verdict degrades the node's own egress.
	seedExit(t, db, live.ID, agentproto.ProbeVerdictSick, time.Minute)
	// A suspended node's fresh healthy exit verdict is healthy: pool state is
	// about entry-pool membership, not the node's egress.
	seedExit(t, db, susp.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	got = Evaluate(db, []storage.Node{live, susp}, now)
	if got[live.ID] != Degraded {
		t.Fatalf("sick exit verdict = %q", got[live.ID])
	}
	if got[susp.ID] != Healthy {
		t.Fatalf("suspended with healthy exit = %q", got[susp.ID])
	}

	// A fresh healthy verdict lifts the degraded node's egress: one healthy
	// against one sick is a tie, and under the F-0931 hysteresis ties count
	// healthy (the burden of proof for degrading is a sick majority).
	seedExit(t, db, live.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	got = Evaluate(db, []storage.Node{live}, now)
	if got[live.ID] != Healthy {
		t.Fatalf("recovered exit = %q", got[live.ID])
	}

	// Stale verdicts only fall back to unknown.
	seedExit(t, db, live.ID, agentproto.ProbeVerdictSick, ProbeFresh+time.Minute)
	got = Evaluate(db, []storage.Node{live}, now)
	if got[live.ID] != Healthy {
		t.Fatalf("stale sick must not outweigh fresh healthy = %q", got[live.ID])
	}
	stale := mkNode(t, db, "eg-stale", "online", "active", true)
	seedExit(t, db, stale.ID, agentproto.ProbeVerdictHealthy, ProbeFresh+time.Minute)
	got = Evaluate(db, []storage.Node{stale}, now)
	if got[stale.ID] != Unknown {
		t.Fatalf("stale verdict only = %q", got[stale.ID])
	}
}

// TestEvaluateHysteresis pins the F-0931 window majority for the exit
// baseline: a single lost probe must not flap the node's own egress, and a
// single stray success must not whitewash a mostly-broken egress.
func TestEvaluateHysteresis(t *testing.T) {
	db := newTestDB(t)
	node := mkNode(t, db, "eg-hyst", "online", "active", true)
	prober := mkNode(t, db, "eg-hyst-b", "online", "active", true)

	now := time.Now()
	// 3 healthy + 1 stray sick: still healthy.
	for i := 0; i < 3; i++ {
		seedExit(t, db, node.ID, agentproto.ProbeVerdictHealthy, time.Duration(i+1)*time.Minute)
	}
	seedExit(t, db, node.ID, agentproto.ProbeVerdictSick, time.Minute)
	got := Evaluate(db, []storage.Node{node}, now)
	if got[node.ID] != Healthy {
		t.Fatalf("one stray sick in a healthy exit window = %q", got[node.ID])
	}

	// 3 sick + 1 stray healthy: still degraded.
	for i := 0; i < 3; i++ {
		seedExit(t, db, prober.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
	}
	seedExit(t, db, prober.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	got = Evaluate(db, []storage.Node{prober}, now)
	if got[prober.ID] != Degraded {
		t.Fatalf("one stray healthy in a sick exit window = %q", got[prober.ID])
	}
}
