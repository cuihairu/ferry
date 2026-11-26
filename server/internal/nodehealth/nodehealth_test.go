package nodehealth

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
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "nodehealth.db"))
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

func seedVerdict(t *testing.T, db *gorm.DB, prober, target uint, verdict string, offset time.Duration) {
	t.Helper()
	if err := db.Create(&storage.ProbeReport{
		NodeID: prober, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: verdict, ProbedAt: time.Now().Add(-offset),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// TestEvaluateRules covers F-0902's decision table: disabled/offline is off,
// suspended degrades, fresh verdicts decide the rest, and absence of verdicts
// is unknown -- never invented health.
func TestEvaluateRules(t *testing.T) {
	db := newTestDB(t)
	live := mkNode(t, db, "nh-live", "online", "active", true)
	susp := mkNode(t, db, "nh-susp", "online", "suspended", true)
	off := mkNode(t, db, "nh-off", "offline", "active", true)
	dis := mkNode(t, db, "nh-dis", "online", "active", false)

	now := time.Now()
	// gorm's default:true swallows a false Enabled on Create and the sqlite
	// RETURNING clause even refills the struct; pin DB and struct explicitly.
	if err := db.Model(&storage.Node{}).Where("id = ?", dis.ID).
		UpdateColumn("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	dis.Enabled = false
	got := Evaluate(db, []storage.Node{live, susp, off, dis}, now)
	if got[live.ID] != Unknown {
		t.Fatalf("live without verdicts = %q (absence is not health)", got[live.ID])
	}
	if got[susp.ID] != Degraded {
		t.Fatalf("suspended = %q", got[susp.ID])
	}
	if got[off.ID] != Off {
		t.Fatalf("offline = %q", got[off.ID])
	}
	if got[dis.ID] != Off {
		t.Fatalf("disabled = %q", got[dis.ID])
	}

	// A fresh healthy verdict makes the live node healthy and lifts a
	// suspended node only to degraded (pool state outranks verdicts).
	seedVerdict(t, db, off.ID, live.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	seedVerdict(t, db, off.ID, susp.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	got = Evaluate(db, []storage.Node{live, susp}, now)
	if got[live.ID] != Healthy {
		t.Fatalf("live with healthy verdict = %q", got[live.ID])
	}
	if got[susp.ID] != Degraded {
		t.Fatalf("suspended with healthy verdict = %q", got[susp.ID])
	}

	// A fresh sick verdict degrades the live node.
	seedVerdict(t, db, off.ID, live.ID, agentproto.ProbeVerdictSick, time.Minute)
	got = Evaluate(db, []storage.Node{live}, now)
	if got[live.ID] != Degraded {
		t.Fatalf("live with sick verdict = %q", got[live.ID])
	}

	// Stale verdicts fall back to unknown for a live node.
	seedVerdict(t, db, off.ID, live.ID, agentproto.ProbeVerdictHealthy, ProbeFresh+time.Minute)
	// the stale one is older, so the fresh sick above still wins -- use a
	// clean node to isolate the staleness rule.
	stale := mkNode(t, db, "nh-stale", "online", "active", true)
	seedVerdict(t, db, off.ID, stale.ID, agentproto.ProbeVerdictHealthy, ProbeFresh+time.Minute)
	got = Evaluate(db, []storage.Node{stale}, now)
	if got[stale.ID] != Unknown {
		t.Fatalf("stale verdict only = %q", got[stale.ID])
	}
}
