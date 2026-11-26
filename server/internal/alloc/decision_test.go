package alloc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// testNow pins the decision clock at noon -- outside the default off-peak
// window (1-7), so the peak switch margins are active.
var testNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.Local)

// TestBuildRequestLoadsSignals covers F-1022: one call loads the whole input
// model -- policy, pools, load and health signals, manual flags and current
// auto rows -- so the decision never queries per entry.
func TestBuildRequestLoadsSignals(t *testing.T) {
	db := newTestDB(t)

	entry := seedNode(t, db, "br-entry", "entry", "out", nil)
	manual := seedNode(t, db, "br-manual", "entry", "out", nil)
	loading := seedNode(t, db, "br-loading", "landing", "out", nil)
	sickly := seedNode(t, db, "br-sick", "landing", "out", nil)

	seedConns(t, db, loading.ID, "relay", 7)
	seedProbe(t, db, sickly.ID, "sick")

	// current auto row for the entry, effective manual row for the other
	eid, mid := entry.ID, manual.ID
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &eid, LandingNodeID: loading.ID, Direction: "out",
		Strategy: "cost_first", AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &mid, LandingNodeID: sickly.ID, Direction: "out",
		Strategy: "manual", AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	req, err := BuildRequest(db, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Entries) != 2 || len(req.Landings) != 2 {
		t.Fatalf("pools = %d entries, %d landings", len(req.Entries), len(req.Landings))
	}
	if req.Conns[loading.ID] != 7 {
		t.Fatalf("conns = %v", req.Conns)
	}
	if !req.Sick[sickly.ID] || req.Sick[loading.ID] {
		t.Fatalf("sick = %v", req.Sick)
	}
	if !req.Manual[manual.ID] || req.Manual[entry.ID] {
		t.Fatalf("manual = %v", req.Manual)
	}
	if cur := req.Current[entry.ID]; cur == nil || cur.LandingNodeID != loading.ID {
		t.Fatalf("current = %+v", cur)
	}
	if req.Policy.Out == "" {
		t.Fatalf("policy not loaded: %+v", req.Policy)
	}
}

// TestDecidePureModel covers F-1023: Decide works on a hand-built Request
// with no database at all -- one decision per entry, candidates ranked by the
// policy, ceiling-excluded candidates carried as exclusions, the winner and
// the switch action spelled out.
func TestDecidePureModel(t *testing.T) {
	// IDs are arbitrary in the pure model; only map lookups matter.
	cheapNode := storage.Node{ID: 11, Name: "cheap", Role: "landing", Direction: "out",
		BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	priceyNode := storage.Node{ID: 12, Name: "pricey", Role: "landing", Direction: "out",
		BillingType: "按流量", TrafficPriceCents: 50, UpdatedAt: testNow,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	overNode := storage.Node{ID: 13, Name: "over", Role: "landing", Direction: "out",
		BillingType: "按流量", TrafficPriceCents: 200, UpdatedAt: testNow,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	entryNode := storage.Node{ID: 21, Name: "entry", Role: "entry", Direction: "out",
		Capabilities: storage.MustJSON([]string{"proxy_ingress", "relay_entry", "probe"})}

	req := &Request{
		Now: testNow,
		Policy: func() PolicySetting {
			p := DefaultPolicySetting()
			p.Out = PolicyCostFirst
			p.MaxCostPerGB = 100
			return p
		}(),
		Entries:  []storage.Node{entryNode},
		Landings: []storage.Node{cheapNode, priceyNode, overNode},
		Conns:    map[uint]int{},
		Sick:     map[uint]bool{12: true}, // the pricey landing is sick
		Manual:   map[uint]bool{},
		Current:  map[uint]*storage.LandingAssignment{},
	}

	decs := Decide(req)
	if len(decs) != 1 {
		t.Fatalf("decisions = %d", len(decs))
	}
	d := decs[0]
	if d.EntryID != 21 || d.Direction != "out" || d.Policy != PolicyCostFirst {
		t.Fatalf("header = %+v", d)
	}
	if d.Action != ActionSwitch || d.WinnerID != 11 || d.ToID != 11 || d.FromID != 0 {
		t.Fatalf("action = %+v", d)
	}
	if d.Reason == "" {
		t.Fatalf("switch reason empty")
	}
	// the ceiling-excluded landing is an exclusion, not a candidate
	if len(d.Exclusions) != 1 || d.Exclusions[0].LandingID != 13 || d.Exclusions[0].Reason == "" {
		t.Fatalf("exclusions = %+v", d.Exclusions)
	}
	// candidates ranked: cheap healthy first, sick pricey second with its reason
	if len(d.Candidates) != 2 {
		t.Fatalf("candidates = %+v", d.Candidates)
	}
	if d.Candidates[0].Candidate.Node.ID != 11 || !d.Candidates[0].Eligible {
		t.Fatalf("rank[0] = %+v", d.Candidates[0])
	}
	if d.Candidates[1].Candidate.Node.ID != 12 || d.Candidates[1].Eligible ||
		d.Candidates[1].Reason != "probe window reports sick" {
		t.Fatalf("rank[1] = %+v", d.Candidates[1])
	}
}

// TestDecideKeepNoneSkip covers the non-switch actions: keep when the current
// landing is already the winner, none when every candidate is sick, skip when
// the entry cannot run the relay data plane (its exclusions still travel with
// the decision so the executor can trail them).
func TestDecideKeepNoneSkip(t *testing.T) {
	a := storage.Node{ID: 31, Name: "a", Role: "landing", Direction: "out",
		BillingType: "按流量", TrafficPriceCents: 5, UpdatedAt: testNow,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	b := storage.Node{ID: 32, Name: "b", Role: "landing", Direction: "out",
		BillingType: "按流量", TrafficPriceCents: 50, UpdatedAt: testNow,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	entry := storage.Node{ID: 41, Name: "entry", Role: "entry", Direction: "out",
		Capabilities: storage.MustJSON([]string{"proxy_ingress", "relay_entry", "probe"})}

	base := func() *Request {
		return &Request{
			Now: testNow,
			Policy: func() PolicySetting {
				p := DefaultPolicySetting()
				p.Out = PolicyCostFirst
				return p
			}(),
			Entries:  []storage.Node{entry},
			Landings: []storage.Node{a, b},
			Conns:    map[uint]int{},
			Sick:     map[uint]bool{},
			Manual:   map[uint]bool{},
			Current:  map[uint]*storage.LandingAssignment{},
		}
	}

	// current is already the winner -> keep
	req := base()
	req.Current[41] = &storage.LandingAssignment{ID: 9, LandingNodeID: 31}
	decs := Decide(req)
	if len(decs) != 1 || decs[0].Action != ActionKeep || decs[0].WinnerID != 31 {
		t.Fatalf("keep decisions = %+v", decs)
	}

	// peak margin not met: the winner at 40 is under the current 45 but the
	// saving is not the 30% the peak check demands
	req = base()
	req.Current[41] = &storage.LandingAssignment{ID: 9, LandingNodeID: 32}
	req.Landings[0].TrafficPriceCents = 40
	decs = Decide(req)
	if len(decs) != 1 || decs[0].Action != ActionKeep || decs[0].WinnerID != 31 {
		t.Fatalf("peak-margin decisions = %+v", decs)
	}

	// all candidates sick -> none
	req = base()
	req.Sick = map[uint]bool{31: true, 32: true}
	decs = Decide(req)
	if len(decs) != 1 || decs[0].Action != ActionNone || decs[0].WinnerID != 0 {
		t.Fatalf("none decisions = %+v", decs)
	}

	// entry without relay_entry -> skip; ceiling exclusions still collected
	req = base()
	req.Policy.MaxCostPerGB = 10
	req.Entries[0].Capabilities = storage.MustJSON([]string{"probe"})
	decs = Decide(req)
	if len(decs) != 1 || decs[0].Action != ActionSkip || decs[0].Reason == "" {
		t.Fatalf("skip decisions = %+v", decs)
	}
	if len(decs[0].Exclusions) != 1 || decs[0].Exclusions[0].LandingID != 32 {
		t.Fatalf("skip exclusions = %+v", decs[0].Exclusions)
	}

	// a manual entry produces no decision at all
	req = base()
	req.Manual[41] = true
	if decs = Decide(req); len(decs) != 0 {
		t.Fatalf("manual decisions = %+v", decs)
	}
}

// TestApplyWritesTrailsAndRows covers the executor half of F-1023: Apply
// trails exclusions with the dedupe window, releases the current auto row,
// creates the new assignment, and returns the switch events with resolved
// names. A keep decision writes nothing.
func TestApplyWritesTrailsAndRows(t *testing.T) {
	db := newTestDB(t)

	landA := seedNode(t, db, "ap-a", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
	})
	landOver := seedNode(t, db, "ap-over", "landing", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_landing"})
	})
	entry := seedNode(t, db, "ap-entry", "entry", "out", func(n *storage.Node) {
		n.Capabilities = storage.MustJSON([]string{"relay_entry"})
	})

	rid := entry.ID
	cur := &storage.LandingAssignment{EntryNodeID: &rid, LandingNodeID: landOver.ID,
		Direction: "out", Strategy: "cost_first", AssignedAt: testNow}
	if err := db.Create(cur).Error; err != nil {
		t.Fatal(err)
	}

	req := &Request{
		Now: testNow, Policy: DefaultPolicySetting(),
		Entries: []storage.Node{entry}, Landings: []storage.Node{landA, landOver},
		Conns: map[uint]int{}, Sick: map[uint]bool{}, Manual: map[uint]bool{},
		Current: map[uint]*storage.LandingAssignment{entry.ID: cur},
	}
	decs := []Decision{{
		EntryID: entry.ID, EntryName: entry.Name, Direction: "out", Policy: PolicyCostFirst,
		Exclusions: []Exclusion{{LandingID: landOver.ID, Reason: "单价 200 分/GB 超上限 100"}},
		WinnerID:   landA.ID, Action: ActionSwitch, FromID: landOver.ID, ToID: landA.ID,
		Reason: "cost_first 负载 0 连/100M",
	}}

	events, err := Apply(db, req, decs)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	ev := events[0]
	if ev.EntryID != entry.ID || ev.FromName != landOver.Name || ev.ToName != landA.Name {
		t.Fatalf("event = %+v", ev)
	}
	// the old row is released with the trail reason
	var old storage.LandingAssignment
	if err := db.First(&old, cur.ID).Error; err != nil {
		t.Fatal(err)
	}
	if old.ReleasedAt == nil || old.ReleaseReason != "自动换线" {
		t.Fatalf("old row = %+v", old)
	}
	// a new effective row points at the winner
	var rows []storage.LandingAssignment
	if err := db.Where("released_at IS NULL AND entry_node_id = ?", entry.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].LandingNodeID != landA.ID || rows[0].Strategy != PolicyCostFirst {
		t.Fatalf("rows = %+v", rows)
	}
	// the exclusion is trailed
	var trails []storage.AllocExclusion
	if err := db.Where("entry_node_id = ?", entry.ID).Find(&trails).Error; err != nil {
		t.Fatal(err)
	}
	if len(trails) != 1 || trails[0].LandingNodeID != landOver.ID || trails[0].Reason == "" {
		t.Fatalf("trails = %+v", trails)
	}
	// a keep decision writes nothing further
	if _, err = Apply(db, req, []Decision{{EntryID: entry.ID, Action: ActionKeep}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("released_at IS NULL AND entry_node_id = ?", entry.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("keep wrote rows: %+v", rows)
	}
}

// TestApplyRecordsSnapshot covers the F-1032 alloc side: a round that moved
// something records one snapshot with the replay rows inline (names, action,
// reason), and a keep-only round records nothing.
func TestApplyRecordsSnapshot(t *testing.T) {
	db := newTestDB(t)

	landA := seedNode(t, db, "sn-a", "landing", "out", nil)
	landB := seedNode(t, db, "sn-b", "landing", "out", nil)
	entry := seedNode(t, db, "sn-entry", "entry", "out", nil)

	rid := entry.ID
	cur := &storage.LandingAssignment{EntryNodeID: &rid, LandingNodeID: landB.ID,
		Direction: "out", Strategy: PolicyCostFirst, AssignedAt: testNow}
	if err := db.Create(cur).Error; err != nil {
		t.Fatal(err)
	}
	req := &Request{
		Now: testNow, Policy: DefaultPolicySetting(),
		Entries: []storage.Node{entry}, Landings: []storage.Node{landA, landB},
		Conns: map[uint]int{}, Sick: map[uint]bool{}, Manual: map[uint]bool{},
		Current: map[uint]*storage.LandingAssignment{entry.ID: cur},
	}
	decs := []Decision{{
		EntryID: entry.ID, EntryName: entry.Name, Direction: "out", Policy: PolicyCostFirst,
		WinnerID: landA.ID, Action: ActionSwitch, FromID: landB.ID, ToID: landA.ID,
		Reason: "cost_first 负载 0 连/100M",
	}}
	if _, err := Apply(db, req, decs); err != nil {
		t.Fatal(err)
	}

	var snaps []storage.AllocDecision
	if err := db.Find(&snaps).Error; err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d", len(snaps))
	}
	s := snaps[0]
	if s.Kind != SnapshotKindAlloc || s.Switches != 1 || !s.RoundAt.Equal(testNow) {
		t.Fatalf("snapshot header = %+v", s)
	}
	if s.PolicyOut != PolicyCostFirst || s.Entries != 1 || s.Landings != 2 {
		t.Fatalf("snapshot counts = %+v", s)
	}
	var replay []decisionSnapshotRow
	if err := json.Unmarshal([]byte(s.Decisions), &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay) != 1 || replay[0].Action != ActionSwitch ||
		replay[0].EntryName != entry.Name || replay[0].From != landB.Name || replay[0].To != landA.Name ||
		replay[0].Reason == "" {
		t.Fatalf("snapshot rows = %+v", replay)
	}

	// a keep-only round appends no snapshot
	if _, err := Apply(db, req, []Decision{{EntryID: entry.ID, Action: ActionKeep}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Find(&snaps).Error; err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("keep round wrote snapshots = %d", len(snaps))
	}
}
