package transport

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestJudgeEmptyLatest 覆盖无任何新鲜结论时返回空行。
func TestJudgeEmptyLatest(t *testing.T) {
	db := newDB(t)
	rows, err := Judge(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty judge rows = %+v", rows)
	}
}

// TestJudgeUnknownRegion 覆盖节点缺 region 归"未知"且无 fresh 数据仍出维。
func TestJudgeUnknownRegion(t *testing.T) {
	db := newDB(t)
	n := seedNode(t, db, "no-region", "", "tls")
	report(t, db, 99, n.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	rows, err := Judge(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Region != "未知" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestBetterTieBreak 覆盖 better 的占比/存活数/字典序平局。
func TestBetterTieBreak(t *testing.T) {
	// 存活占比同、存活数同 → 字典序小者胜。
	a := TransportHealth{Transport: "b", Alive: 1, Total: 2}
	b := TransportHealth{Transport: "a", Alive: 1, Total: 2}
	if !better(b, a) {
		t.Fatal("dict order should win tie")
	}
	// 存活数更高胜。
	c := TransportHealth{Transport: "z", Alive: 2, Total: 2}
	if !better(c, a) {
		t.Fatal("more alive should win")
	}
	// 占比更高胜。
	d := TransportHealth{Transport: "a", Alive: 2, Total: 3}
	if !better(c, d) {
		t.Fatal("higher ratio should win")
	}
	// 相等返回 false。
	if better(a, a) {
		t.Fatal("equal should be false")
	}
}

// TestJudgeSkipsUnusableInputs 无指向的报告、无传输标注的节点跳过；
// 区域为空的节点归「未知」聚合。
func TestJudgeSkipsUnusableInputs(t *testing.T) {
	db := newDB(t)
	// 区域为空的节点（transport 已标）→ 归「未知」
	blank := seedNode(t, db, "blank-region", "", "tls")
	report(t, db, 99, blank.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	// 无传输标注的节点：即使有报告也不参与（列默认值会填 tls，显式清空）
	noTx := seedNode(t, db, "no-transport", "us", "tls")
	if err := db.Model(&storage.Node{}).Where("id = ?", noTx.ID).
		Update("transport", "").Error; err != nil {
		t.Fatal(err)
	}
	report(t, db, 99, noTx.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	// 无 TargetNodeID 的杂散报告：跳过
	if err := db.Create(&storage.ProbeReport{NodeID: 99, TargetKind: agentproto.ProbeTargetPeer,
		Verdict: agentproto.ProbeVerdictHealthy, ProbedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	rows, err := Judge(db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v; want single 未知 row", rows)
	}
	if rows[0].Region != "未知" {
		t.Fatalf("row = %+v; want 未知 region", rows[0])
	}
	if len(rows[0].Transports) != 1 || rows[0].Transports[0].Transport != "tls" {
		t.Fatalf("transports = %+v; want single tls", rows[0].Transports)
	}
}
