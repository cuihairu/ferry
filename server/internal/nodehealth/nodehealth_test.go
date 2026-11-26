package nodehealth

import (
	"path/filepath"
	"strings"
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

	// F-0931: a healthy and a sick verdict tie, and ties stay healthy; a
	// majority of sick verdicts is needed to degrade the live node.
	seedVerdict(t, db, off.ID, live.ID, agentproto.ProbeVerdictSick, time.Minute)
	got = Evaluate(db, []storage.Node{live}, now)
	if got[live.ID] != Healthy {
		t.Fatalf("tied verdicts must stay healthy = %q", got[live.ID])
	}
	seedVerdict(t, db, off.ID, live.ID, agentproto.ProbeVerdictSick, time.Minute)
	got = Evaluate(db, []storage.Node{live}, now)
	if got[live.ID] != Degraded {
		t.Fatalf("sick majority = %q", got[live.ID])
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

// TestEvaluateHysteresis pins the F-0931 window majority: one stray sick
// report must not flap a mostly-healthy node, and one stray success must not
// whitewash a mostly-sick one.
func TestEvaluateHysteresis(t *testing.T) {
	db := newTestDB(t)
	node := mkNode(t, db, "nh-hyst", "online", "active", true)
	prober := mkNode(t, db, "nh-hyst-prober", "online", "active", true)

	now := time.Now()
	// 3 healthy + 1 stray sick: still healthy.
	for i := 0; i < 3; i++ {
		seedVerdict(t, db, prober.ID, node.ID, agentproto.ProbeVerdictHealthy, time.Duration(i+1)*time.Minute)
	}
	seedVerdict(t, db, prober.ID, node.ID, agentproto.ProbeVerdictSick, time.Minute)
	got := Evaluate(db, []storage.Node{node}, now)
	if got[node.ID] != Healthy {
		t.Fatalf("one stray sick in a healthy window = %q", got[node.ID])
	}

	// 3 sick + 1 stray healthy: still degraded.
	other := mkNode(t, db, "nh-hyst-sick", "online", "active", true)
	for i := 0; i < 3; i++ {
		seedVerdict(t, db, prober.ID, other.ID, agentproto.ProbeVerdictSick, time.Duration(i+1)*time.Minute)
	}
	seedVerdict(t, db, prober.ID, other.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	got = Evaluate(db, []storage.Node{other}, now)
	if got[other.ID] != Degraded {
		t.Fatalf("one stray healthy in a sick window = %q", got[other.ID])
	}
}

// TestEvaluateOverride covers the F-0932 manual override: an active override
// wins over the computed state, an expired one stops applying on its own, and
// an override may not whitewash a disabled node out of off.
func TestEvaluateOverride(t *testing.T) {
	db := newTestDB(t)
	node := mkNode(t, db, "nh-ovr", "online", "active", true)
	off := mkNode(t, db, "nh-ovr-off", "offline", "active", true)
	now := time.Now()

	// No verdicts: computed unknown; forced healthy wins anyway.
	mkOverride := func(n storage.Node, state string, expires time.Time) {
		t.Helper()
		// one row per node, same replace rule the handler applies
		if err := db.Where("node_id = ?", n.ID).Delete(&storage.HealthOverride{}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&storage.HealthOverride{
			NodeID: n.ID, State: state, Reason: "ops window", Operator: "oncall",
			ExpiresAt: expires,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkOverride(node, "healthy", now.Add(time.Hour))
	got := Evaluate(db, []storage.Node{node, off}, now)
	if got[node.ID] != Healthy {
		t.Fatalf("active override = %q; want healthy", got[node.ID])
	}
	// Off is not overridable: the node is out of service regardless.
	if got[off.ID] != Off {
		t.Fatalf("override must not lift off = %q", got[off.ID])
	}

	// Forced degraded pins a node whose probe window is healthy.
	prober := mkNode(t, db, "nh-ovr-prober", "online", "active", true)
	seedVerdict(t, db, prober.ID, node.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	mkOverride(node, "degraded", now.Add(time.Hour))
	got = Evaluate(db, []storage.Node{node}, now)
	if got[node.ID] != Degraded {
		t.Fatalf("forced degraded = %q; want degraded over healthy window", got[node.ID])
	}

	// An expired override stops applying: the computed state resumes.
	if err := db.Model(&storage.HealthOverride{}).Where("node_id = ?", node.ID).
		UpdateColumn("expires_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	got = Evaluate(db, []storage.Node{node}, now)
	if got[node.ID] != Healthy {
		t.Fatalf("expired override must not apply = %q; want computed healthy", got[node.ID])
	}
}

// TestEvaluateWithReason pins the F-0934 explanation: the reason names the
// decisive input the same way the state was decided -- absence of evidence,
// pool state, probe counts, override audit.
func TestEvaluateWithReason(t *testing.T) {
	db := newTestDB(t)
	quiet := mkNode(t, db, "nh-r-quiet", "online", "active", true)
	susp := mkNode(t, db, "nh-r-susp", "online", "suspended", true)
	off := mkNode(t, db, "nh-r-off", "offline", "active", true)
	now := time.Now()

	got := EvaluateWithReason(db, []storage.Node{quiet, susp, off}, now)
	if got[quiet.ID].State != Unknown || got[quiet.ID].Reason != "no fresh probe verdicts (30m window)" {
		t.Fatalf("quiet reason = %+v", got[quiet.ID])
	}
	if got[susp.ID].State != Degraded || got[susp.ID].Reason != "pool suspended" {
		t.Fatalf("suspended reason = %+v", got[susp.ID])
	}
	if got[off.ID].State != Off || got[off.ID].Reason != "agent offline (no heartbeat)" {
		t.Fatalf("offline reason = %+v", got[off.ID])
	}

	// A sick majority reports its counts; the override names its operator.
	prober := mkNode(t, db, "nh-r-prober", "online", "active", true)
	sick := mkNode(t, db, "nh-r-sick", "online", "active", true)
	seedVerdict(t, db, prober.ID, sick.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	seedVerdict(t, db, prober.ID, sick.ID, agentproto.ProbeVerdictSick, time.Minute)
	seedVerdict(t, db, prober.ID, sick.ID, agentproto.ProbeVerdictSick, 2*time.Minute)
	if err := db.Create(&storage.HealthOverride{
		NodeID: sick.ID, State: "degraded", Reason: "suspected flapping",
		Operator: "neteng", ExpiresAt: now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatal(err)
	}
	got = EvaluateWithReason(db, []storage.Node{sick}, now)
	if got[sick.ID].State != Degraded {
		t.Fatalf("override state = %q", got[sick.ID].State)
	}
	if !strings.Contains(got[sick.ID].Reason, "manual override by neteng") ||
		!strings.Contains(got[sick.ID].Reason, "suspected flapping") {
		t.Fatalf("override reason = %q", got[sick.ID].Reason)
	}

	// Without the override the probe counts are the explanation.
	if err := db.Where("node_id = ?", sick.ID).Delete(&storage.HealthOverride{}).Error; err != nil {
		t.Fatal(err)
	}
	got = EvaluateWithReason(db, []storage.Node{sick}, now)
	if got[sick.ID].State != Degraded ||
		got[sick.ID].Reason != "probe window: 2 sick vs 1 healthy (30m)" {
		t.Fatalf("probe reason = %+v", got[sick.ID])
	}
}
