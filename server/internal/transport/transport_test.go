package transport

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNode(t *testing.T, db *gorm.DB, name, region, tx string) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Role: "entry", Enabled: true, Region: region, Transport: tx}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func report(t *testing.T, db *gorm.DB, reporter, target uint, verdict string, age time.Duration) {
	t.Helper()
	if err := db.Create(&storage.ProbeReport{
		NodeID: reporter, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: verdict, ProbedAt: time.Now().Add(-age),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// TestJudge 覆盖区域传输判定（E-17）：存活占比最高者为推荐；
// 现行主流取节点数最多；建议 != 现行时给切换标记。
func TestJudge(t *testing.T) {
	db := newDB(t)
	// hk 区域：tls 两个节点（一病一好），ws-tls 一个节点（好）
	hkTls1 := seedNode(t, db, "hk-tls-1", "hk", "tls")
	hkTls2 := seedNode(t, db, "hk-tls-2", "hk", "tls")
	hkWs := seedNode(t, db, "hk-ws", "hk", "ws-tls")
	report(t, db, 99, hkTls1.ID, agentproto.ProbeVerdictSick, time.Minute)
	report(t, db, 99, hkTls2.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	report(t, db, 99, hkWs.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	// us 区域：只有 quic，全好
	usQ := seedNode(t, db, "us-q", "us", "quic")
	report(t, db, 99, usQ.ID, agentproto.ProbeVerdictHealthy, time.Minute)

	rows, err := Judge(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	byRegion := map[string]Row{}
	for _, r := range rows {
		byRegion[r.Region] = r
	}
	hk := byRegion["hk"]
	if hk.Current != "tls" {
		t.Fatalf("hk current = %s, want tls (2 nodes)", hk.Current)
	}
	if hk.Recommended != "ws-tls" {
		t.Fatalf("hk recommended = %s, want ws-tls (100%% alive)", hk.Recommended)
	}
	if !hk.Switch {
		t.Fatal("hk should suggest switch")
	}
	us := byRegion["us"]
	if us.Recommended != "quic" || us.Switch {
		t.Fatalf("us = %+v", us)
	}
}

// TestJudgeStaleAndMissing 覆盖口径边界：超龄结论不参与；
// 无新鲜结论的区域不出行；最新结论覆盖旧结论。
func TestJudgeStaleAndMissing(t *testing.T) {
	db := newDB(t)
	stale := seedNode(t, db, "stale", "jp", "tls")
	report(t, db, 99, stale.ID, agentproto.ProbeVerdictHealthy, DefaultWindow+time.Minute)

	flip := seedNode(t, db, "flip", "jp", "ws-tls")
	report(t, db, 99, flip.ID, agentproto.ProbeVerdictSick, 4*time.Minute)
	report(t, db, 99, flip.ID, agentproto.ProbeVerdictHealthy, time.Minute)

	rows, err := Judge(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Region != "jp" {
		t.Fatalf("rows = %+v, want only jp", rows)
	}
	r := rows[0]
	if len(r.Transports) != 1 || r.Transports[0].Transport != "ws-tls" {
		t.Fatalf("transports = %+v, stale tls node must be excluded", r.Transports)
	}
	if r.Transports[0].Alive != 1 {
		t.Fatalf("latest verdict must win: %+v", r.Transports[0])
	}
}
