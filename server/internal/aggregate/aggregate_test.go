package aggregate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// seed stores one probe verdict.
func seed(t *testing.T, db *gorm.DB, target *uint, kind, region, isp, verdict string, at time.Time) {
	t.Helper()
	r := storage.ProbeReport{
		NodeID:       99, // the prober
		TargetKind:   kind,
		TargetNodeID: target,
		Direction:    agentproto.DirectionOut,
		Verdict:      verdict,
		Region:       region,
		ISP:          isp,
		ProbedAt:     at,
	}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("seed probe report: %v", err)
	}
}

func TestJudgeRegionAndISP(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	// East China 4 nodes: 2 sick → region failure (sick count >= 2)
	n1, n2, n3, n7 := uint(1), uint(2), uint(3), uint(7)
	seed(t, db, &n1, agentproto.ProbeTargetTunnel, "华东", "电信", agentproto.ProbeVerdictHealthy, now.Add(-2*time.Minute))
	seed(t, db, &n2, agentproto.ProbeTargetTunnel, "华东", "电信", agentproto.ProbeVerdictSick, now.Add(-2*time.Minute))
	seed(t, db, &n3, agentproto.ProbeTargetTunnel, "华东", "联通", agentproto.ProbeVerdictSick, now.Add(-2*time.Minute))
	seed(t, db, &n7, agentproto.ProbeTargetTunnel, "华东", "联通", agentproto.ProbeVerdictHealthy, now.Add(-2*time.Minute))
	// North China 3 nodes: 1 sick → healthy (1 < 2 and ratio 1/3 < 50%)
	n4, n5, n6 := uint(4), uint(5), uint(6)
	seed(t, db, &n4, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictHealthy, now.Add(-2*time.Minute))
	seed(t, db, &n5, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictSick, now.Add(-2*time.Minute))
	seed(t, db, &n6, agentproto.ProbeTargetTunnel, "华北", "电信", agentproto.ProbeVerdictHealthy, now.Add(-2*time.Minute))

	verdicts, err := Judge(db, Options{})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	want := map[string]Verdict{
		"region/华东": {Dimension: DimensionRegion, Scope: "华东", Sick: 2, Total: 4, Failed: true, SickNodes: []uint64{2, 3}},
		"region/华北": {Dimension: DimensionRegion, Scope: "华北", Sick: 1, Total: 3, Failed: false},
		"isp/电信":    {Dimension: DimensionISP, Scope: "电信", Sick: 2, Total: 5, Failed: true, SickNodes: []uint64{2, 5}},
		"isp/联通":    {Dimension: DimensionISP, Scope: "联通", Sick: 1, Total: 2, Failed: true},
	}
	if len(verdicts) != len(want) {
		t.Fatalf("verdicts = %d, want %d: %+v", len(verdicts), len(want), verdicts)
	}
	for _, v := range verdicts {
		key := string(v.Dimension) + "/" + v.Scope
		w, ok := want[key]
		if !ok {
			t.Fatalf("unexpected verdict: %+v", v)
		}
		if v.Sick != w.Sick || v.Total != w.Total || v.Failed != w.Failed {
			t.Fatalf("verdict %s: got sick=%d total=%d failed=%v, want sick=%d total=%d failed=%v",
				key, v.Sick, v.Total, v.Failed, w.Sick, w.Total, w.Failed)
		}
		if key == "region/华东" && (len(v.SickNodes) != 2 || v.SickNodes[0] != 2 || v.SickNodes[1] != 3) {
			t.Fatalf("sick nodes = %v, want [2 3]", v.SickNodes)
		}
	}
}

func TestJudgeWindowAndLatest(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	var ten uint = 10

	// A sick verdict outside the window does not count
	seed(t, db, &ten, agentproto.ProbeTargetTunnel, "华南", "电信", agentproto.ProbeVerdictSick, now.Add(-10*time.Minute))
	// Exit baseline (no target node) does not take part in the aggregation
	seed(t, db, nil, agentproto.ProbeTargetExit, "华南", "电信", agentproto.ProbeVerdictSick, now.Add(-1*time.Minute))
	// The same target takes the latest verdict: sick first, healthy after → recorded as healthy
	seed(t, db, &ten, agentproto.ProbeTargetTunnel, "华南", "电信", agentproto.ProbeVerdictSick, now.Add(-2*time.Minute))
	seed(t, db, &ten, agentproto.ProbeTargetTunnel, "华南", "电信", agentproto.ProbeVerdictHealthy, now.Add(-1*time.Minute))

	verdicts, err := Judge(db, Options{})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if len(verdicts) != 2 { // one each for region/South China and isp/Telecom
		t.Fatalf("verdicts = %+v", verdicts)
	}
	for _, v := range verdicts {
		if v.Scope != "华南" && v.Scope != "电信" {
			t.Fatalf("unexpected scope %q", v.Scope)
		}
		if v.Sick != 0 || v.Total != 1 || v.Failed {
			t.Fatalf("verdict %s: %+v", v.Scope, v)
		}
	}
}

func TestJudgeEmpty(t *testing.T) {
	db := openTestDB(t)
	verdicts, err := Judge(db, Options{})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("verdicts = %+v", verdicts)
	}
}
