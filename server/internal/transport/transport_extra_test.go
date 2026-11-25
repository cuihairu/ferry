package transport

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestJudgeEmptyLatest returns an empty row when there is no fresh conclusion at all.
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

// TestJudgeUnknownRegion covers nodes with a missing region falling to "unknown" while still emitting a row without fresh data.
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

// TestBetterTieBreak covers better's ties on share / alive count / lexicographic order.
func TestBetterTieBreak(t *testing.T) {
	// Same survival share and same alive count -> the lexicographically smaller one wins.
	a := TransportHealth{Transport: "b", Alive: 1, Total: 2}
	b := TransportHealth{Transport: "a", Alive: 1, Total: 2}
	if !better(b, a) {
		t.Fatal("dict order should win tie")
	}
	// The higher alive count wins.
	c := TransportHealth{Transport: "z", Alive: 2, Total: 2}
	if !better(c, a) {
		t.Fatal("more alive should win")
	}
	// The higher share wins.
	d := TransportHealth{Transport: "a", Alive: 2, Total: 3}
	if !better(c, d) {
		t.Fatal("higher ratio should win")
	}
	// Equal returns false.
	if better(a, a) {
		t.Fatal("equal should be false")
	}
}

// TestJudgeSkipsUnusableInputs a report with no target node and a node with no transport annotation are skipped;
// a region with an empty name is aggregated under "unknown".
func TestJudgeSkipsUnusableInputs(t *testing.T) {
	db := newDB(t)
	// a node with an empty region (transport already annotated) -> "unknown"
	blank := seedNode(t, db, "blank-region", "", "tls")
	report(t, db, 99, blank.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	// a node with no transport annotation: it does not participate even with a report (the column default would fill tls, so clear it explicitly)
	noTx := seedNode(t, db, "no-transport", "us", "tls")
	if err := db.Model(&storage.Node{}).Where("id = ?", noTx.ID).
		Update("transport", "").Error; err != nil {
		t.Fatal(err)
	}
	report(t, db, 99, noTx.ID, agentproto.ProbeVerdictHealthy, time.Minute)
	// stray reports with no TargetNodeID: skipped
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
