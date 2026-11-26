package alloc

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newTestDB creates an in-memory DB and migrates.
func newTestDB(t *testing.T) *gorm.DB {
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

// seedNode stores one node; mutate changes fields before storing (capacity/cost/line tier and other scoring inputs).
func seedNode(t *testing.T, db *gorm.DB, name, role, direction string, mutate func(*storage.Node)) storage.Node {
	t.Helper()
	n := storage.Node{Name: name, Token: "tok-" + name, Role: role, Direction: direction, Enabled: true}
	if mutate != nil {
		mutate(&n)
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// seedProbe records one inter-probe verdict (fresh).
func seedProbe(t *testing.T, db *gorm.DB, target uint, verdict string) {
	t.Helper()
	nodeID := &target
	if err := db.Create(&storage.ProbeReport{
		NodeID: 999, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: nodeID,
		Verdict: verdict, ProbedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// seedConns records one node connection sample (fresh).
func seedConns(t *testing.T, db *gorm.DB, node uint, proc string, conns int) {
	t.Helper()
	if err := db.Create(&storage.NodeTrafficLog{
		NodeID: node, Proc: proc, Conns: conns, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPickLeastConnCapacityWeight(t *testing.T) {
	// With equal connections the 3M small pipe has a higher load ratio, so new connections go to the big pipe (capacity weight, 3M gets fewer).
	cands := []Candidate{
		{Node: storage.Node{Name: "small", BwDownMbps: 3}, Conns: 12, Healthy: true},
		{Node: storage.Node{Name: "big", BwDownMbps: 100}, Conns: 12, Healthy: true},
	}
	idx, ok := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "big" {
		t.Fatalf("capacity weight broken: idx=%d ok=%v", idx, ok)
	}

	// Pure load model: the fewer connections wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 100}, Conns: 50, Healthy: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 100}, Conns: 5, Healthy: true},
	}
	idx, ok = Pick(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("least conn broken: idx=%d ok=%v", idx, ok)
	}
}

func TestPickCostFirst(t *testing.T) {
	// Monthly marginal cost 0 beats every metered tier (fixed monthly cost is sunk and stays out of allocation).
	cands := []Candidate{
		{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 30}, Conns: 10, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Conns: 0, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "monthly", BillingType: "包月"}, Conns: 20, Healthy: true},
	}
	idx, ok := Pick(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "monthly" {
		t.Fatalf("monthly should win cost_first: idx=%d ok=%v", idx, ok)
	}

	cands = []Candidate{
		{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 30}, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, ok = Pick(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "cheap" {
		t.Fatalf("cheap should win cost_first: idx=%d ok=%v", idx, ok)
	}
}

func TestPickPerfFirstInOnly(t *testing.T) {
	// Inbound line: premium tiers win even with a higher load.
	cands := []Candidate{
		{Node: storage.Node{Name: "plain-busy", LineType: "163", BwDownMbps: 100}, Conns: 80, Healthy: true},
		{Node: storage.Node{Name: "gia-busy", LineType: "cn2_gia", BwDownMbps: 100}, Conns: 80, Healthy: true},
	}
	idx, ok := Pick(PolicyPerfFirst, "in", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "gia-busy" {
		t.Fatalf("premium line should win perf_first(in): idx=%d ok=%v", idx, ok)
	}

	// Outbound lines ignore the tier split: the lower load wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "plain-busy", LineType: "163", BwDownMbps: 100}, Conns: 80, Healthy: true},
		{Node: storage.Node{Name: "gia-idle", LineType: "cn2_gia", BwDownMbps: 100}, Conns: 8, Healthy: true},
	}
	idx, ok = Pick(PolicyPerfFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "gia-idle" {
		t.Fatalf("out direction should ignore line tier: idx=%d ok=%v", idx, ok)
	}
}

func TestPickBalanced(t *testing.T) {
	// Balanced tier: load and cost combined, inbound premium lines subtracted.
	cands := []Candidate{
		{Node: storage.Node{Name: "cheap-busy", BwDownMbps: 100}, Conns: 90, Healthy: true},
		{Node: storage.Node{Name: "iplc-loaded", LineType: "iplc", BwDownMbps: 100}, Conns: 60, Healthy: true},
	}
	idx, ok := Pick(PolicyBalanced, "in", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "iplc-loaded" {
		t.Fatalf("premium should win balanced(in): idx=%d ok=%v", idx, ok)
	}

	// Without a premium line the lower load wins.
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 100}, Conns: 90, Healthy: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 100}, Conns: 10, Healthy: true},
	}
	idx, ok = Pick(PolicyBalanced, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("idle should win balanced: idx=%d ok=%v", idx, ok)
	}
}

func TestPickHealthGate(t *testing.T) {
	// All sick allocates nothing; sick ones drop out, healthy ones compare per policy.
	cands := []Candidate{{Node: storage.Node{Name: "sick"}, Healthy: false}}
	if _, ok := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands); ok {
		t.Fatal("all-sick candidates must not allocate")
	}
	cands = append(cands, Candidate{Node: storage.Node{Name: "well", BwDownMbps: 100}, Conns: 30, Healthy: true})
	idx, ok := Pick(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "well" {
		t.Fatalf("sick must be excluded: idx=%d ok=%v", idx, ok)
	}
}

func TestPolicyPersistence(t *testing.T) {
	db := newTestDB(t)
	setting, err := LoadPolicy(db)
	// reflect.DeepEqual: PolicySetting carries a map (sched_plan_modes) and so
	// is not comparable with ==.
	if err != nil || !reflect.DeepEqual(setting, DefaultPolicySetting()) {
		t.Fatalf("default policy = %+v err=%v", setting, err)
	}

	setting = PolicySetting{Out: PolicyCostFirst, In: PolicyPerfFirst, RebalanceStart: 2, RebalanceEnd: 6}
	setting.WLoad, setting.WCost, setting.WPremium = DefaultWLoad, DefaultWCost, DefaultWPremium
	setting.MissingCost = DefaultMissingCost
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPolicy(db)
	if err != nil || !reflect.DeepEqual(got, setting) {
		t.Fatalf("roundtrip = %+v err=%v", got, err)
	}

	if err := SavePolicy(db, PolicySetting{Out: "greedy", In: PolicyBalanced, RebalanceStart: 1, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid policy must be rejected")
	}
	if err := SavePolicy(db, PolicySetting{Out: PolicyBalanced, In: PolicyBalanced, RebalanceStart: 7, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid window must be rejected")
	}
}

// TestDirectionalDefaults covers the per-direction default tiers (E-30): unset means outbound uses cost-effectiveness
// (cost_first) and inbound prefers premium lines (perf_first); saving empty tiers means falling back to the direction default.
func TestDirectionalDefaults(t *testing.T) {
	db := newTestDB(t)
	def := DefaultPolicySetting()
	if def.Out != PolicyCostFirst || def.In != PolicyPerfFirst {
		t.Fatalf("directional defaults = %+v", def)
	}

	if err := SavePolicy(db, PolicySetting{Out: PolicyLeastConn, In: PolicyLeastConn, RebalanceStart: 1, RebalanceEnd: 7}); err != nil {
		t.Fatal(err)
	}
	// Both direction tiers empty: each falls back to its default, window kept.
	if err := SavePolicy(db, PolicySetting{RebalanceStart: 2, RebalanceEnd: 6}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPolicy(db)
	if err != nil {
		t.Fatal(err)
	}
	if got.Out != PolicyCostFirst || got.In != PolicyPerfFirst || got.RebalanceStart != 2 || got.RebalanceEnd != 6 {
		t.Fatalf("reset to defaults = %+v", got)
	}

	// Invalid tiers are still rejected (empty ≠ any value).
	if err := SavePolicy(db, PolicySetting{Out: "greedy", In: "", RebalanceStart: 1, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid out must be rejected")
	}
}

func TestSweepAllocatesAndSwitches(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	seedNode(t, db, "land-a", "landing", "out", nil)
	seedNode(t, db, "land-b", "landing", "out", nil)

	// First round: zero load, zero cost, all equal — name lexicographic order gives land-a the win.
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "land-a" || events[0].FromName != "（无）" {
		t.Fatalf("first sweep events = %+v", events)
	}

	// Re-running with no change produces no new rows (no churn).
	if events, err = Sweep(db); err != nil || len(events) != 0 {
		t.Fatalf("no-churn sweep = %+v err=%v", events, err)
	}

	// land-a went sick → auto-switch to land-b, the old row is released with a trail.
	var landA storage.Node
	if err := db.Where("name = ?", "land-a").First(&landA).Error; err != nil {
		t.Fatal(err)
	}
	seedProbe(t, db, landA.ID, agentproto.ProbeVerdictSick)
	events, err = Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "land-b" || events[0].FromName != "land-a" {
		t.Fatalf("switch events = %+v", events)
	}

	var rows []storage.LandingAssignment
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].ReleasedAt == nil || rows[0].ReleaseReason != "自动换线" || rows[0].Reason == "" {
		t.Fatalf("released row = %+v", rows[0])
	}
	if rows[1].ReleasedAt != nil || rows[1].Strategy != PolicyCostFirst {
		t.Fatalf("current row = %+v", rows[1])
	}
}

func TestSweepLoadSignal(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	landA := seedNode(t, db, "land-a", "landing", "out", func(n *storage.Node) { n.BwDownMbps = 3 })
	landB := seedNode(t, db, "land-b", "landing", "out", func(n *storage.Node) { n.BwDownMbps = 100 })

	// land-a (3M small pipe) holds 30 connections (load ratio 10), land-b (100M) holds 30 (0.3):
	// under least_conn the new entry goes to land-b (3M gets fewer).
	seedConns(t, db, landA.ID, "relay", 30)
	seedConns(t, db, landB.ID, "relay", 30)
	if err := SavePolicy(db, PolicySetting{Out: PolicyLeastConn, In: PolicyLeastConn, RebalanceStart: 1, RebalanceEnd: 7}); err != nil {
		t.Fatal(err)
	}

	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "land-b" {
		t.Fatalf("load-aware events = %+v", events)
	}
	var row storage.LandingAssignment
	if err := db.Where("released_at IS NULL").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Strategy != PolicyLeastConn {
		t.Fatalf("strategy = %s", row.Strategy)
	}
}

func TestSweepManualWins(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "entry", "entry", "out", nil)
	landing := seedNode(t, db, "land", "landing", "out", nil)

	// An entry-level manual row blocks auto allocation.
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landing.ID,
		Direction: "out", Strategy: PolicyManual, AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("manual must win: %+v err=%v", events, err)
	}

	// A region-level manual row blocks it the same way.
	if err := db.Exec("DELETE FROM landing_assignments").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).Update("region", "hk").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.LandingAssignment{
		Region: "hk", LandingNodeID: landing.ID,
		Direction: "out", Strategy: PolicyManual, AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("region manual must win: %+v err=%v", events, err)
	}
}

func TestSweepScopeGuards(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "entry", "entry", "out", nil)
	seedNode(t, db, "land", "landing", "out", nil)

	// Suspended entries do not join allocation.
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("suspended entry must be skipped: %+v err=%v", events, err)
	}

	// Disabled landings stay out of the candidate pool: after resetting the entry, the only landing is disabled → no candidates, no move.
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("pool_state", "active").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE nodes SET enabled = 0 WHERE role = 'landing'").Error; err != nil {
		t.Fatal(err)
	}
	events, err = Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("disabled landing must be excluded: %+v err=%v", events, err)
	}
}

func TestSweepAllSickKeepsCurrent(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	landing := seedNode(t, db, "land", "landing", "out", nil)

	events, err := Sweep(db)
	if err != nil || len(events) != 1 {
		t.Fatalf("initial sweep = %+v err=%v", events, err)
	}

	// Landing turned sick: keep the current one, no switch, no new events or rows.
	seedProbe(t, db, landing.ID, agentproto.ProbeVerdictSick)
	events, err = Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("all-sick sweep = %+v err=%v", events, err)
	}
	var n int64
	if err := db.Model(&storage.LandingAssignment{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestShouldSwitchPeakMargins(t *testing.T) {
	// least_conn at peak: the primary metric must save 30% or more to move.
	cur := Candidate{Node: storage.Node{Name: "cur", BwDownMbps: 100}, Conns: 100, Healthy: true}
	slightly := Candidate{Node: storage.Node{Name: "slightly", BwDownMbps: 100}, Conns: 90, Healthy: true}
	much := Candidate{Node: storage.Node{Name: "much", BwDownMbps: 100}, Conns: 50, Healthy: true}
	cands := []Candidate{cur, slightly, much}
	if shouldSwitch(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("10% better must not switch on peak")
	}
	if !shouldSwitch(PolicyLeastConn, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 2) {
		t.Fatal("50% better must switch on peak")
	}

	// cost_first at peak: marginal cost must drop 30% or more to move; a zero-cost monthly current never switches.
	cheap := Candidate{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 90}, Healthy: true, CostKnown: true, CostFresh: true}
	muchCheap := Candidate{Node: storage.Node{Name: "much-cheap", BillingType: "按流量", TrafficPriceCents: 30}, Healthy: true, CostKnown: true, CostFresh: true}
	pricey := Candidate{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Healthy: true, CostKnown: true, CostFresh: true}
	free := Candidate{Node: storage.Node{Name: "free", BillingType: "包月"}, Healthy: true}
	cands = []Candidate{pricey, cheap, muchCheap, free}
	if shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("10% cheaper must not switch on peak")
	}
	if !shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 2) {
		t.Fatal("70% cheaper must switch on peak")
	}
	if !shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 3) {
		t.Fatal("monthly free must switch on peak")
	}
	if shouldSwitch(PolicyCostFirst, "out", MissingCostDeprioritize, DefaultPolicySetting().Weights(), []Candidate{free, pricey}, 0, 1) {
		t.Fatal("free current must never switch on peak")
	}

	// perf_first at peak: only tier upgrades (plain → premium) are allowed, downgrades are not.
	plain := Candidate{Node: storage.Node{Name: "plain", LineType: "163"}, Healthy: true}
	gia := Candidate{Node: storage.Node{Name: "gia", LineType: "cn2_gia"}, Healthy: true}
	cands = []Candidate{plain, gia}
	if !shouldSwitch(PolicyPerfFirst, "in", MissingCostDeprioritize, DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("line upgrade must switch on peak")
	}
	if shouldSwitch(PolicyPerfFirst, "in", MissingCostDeprioritize, DefaultPolicySetting().Weights(), []Candidate{gia, plain}, 0, 1) {
		t.Fatal("line downgrade must not switch on peak")
	}
}

func TestSweepPeakHysteresisAndOffpeakRebalance(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "entry", "entry", "out", nil)
	landA := seedNode(t, db, "land-a", "landing", "out", nil)
	landB := seedNode(t, db, "land-b", "landing", "out", nil)

	// Current land-a (100M floor). land-b has the same capacity but slightly fewer connections: at peak a 10% edge does not move,
	// the off-peak window rebalances whenever the total order is better.
	seedConns(t, db, landA.ID, "relay", 100)
	seedConns(t, db, landB.ID, "relay", 90)
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: landA.ID,
		Direction: "out", Strategy: PolicyLeastConn, AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := SavePolicy(db, PolicySetting{Out: PolicyLeastConn, In: PolicyLeastConn, RebalanceStart: 1, RebalanceEnd: 7}); err != nil {
		t.Fatal(err)
	}

	// At peak (12:00): land-b is only 10% better, no switch.
	events, err := SweepAt(db, time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local))
	if err != nil || len(events) != 0 {
		t.Fatalf("peak slight gain must hold: %+v err=%v", events, err)
	}

	// Off-peak (3:00): a better total order rebalances.
	events, err = SweepAt(db, time.Date(2026, 10, 7, 3, 0, 0, 0, time.Local))
	if err != nil || len(events) != 1 || events[0].ToName != "land-b" {
		t.Fatalf("offpeak rebalance = %+v err=%v", events, err)
	}

	// At peak a much better option: land-c load ratio 0.3 vs current 0.9, clearly better so it moves.
	landC := seedNode(t, db, "land-c", "landing", "out", nil)
	seedConns(t, db, landC.ID, "relay", 30)
	events, err = SweepAt(db, time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local))
	if err != nil || len(events) != 1 || events[0].ToName != "land-c" {
		t.Fatalf("peak big gain must switch: %+v err=%v", events, err)
	}
}

// TestPickBalancedWeights dash-editable weights (scheduler core design §weight composition):
// the defaults 0.5/0.3/0.2 reproduce the current formula; changing a weight changes the result — a higher cost
// weight lets the low cost win, zeroing the cost weight degenerates to pure load ratio, negative/all-zero weights fall back to the default.
func TestPickBalancedWeights(t *testing.T) {
	// cheap-idle (low load, low zero cost) vs busy-premium (high load, premium line, low-value subtraction):
	// with the defaults the premium subtraction of 0.2 cannot cover the load gap, so idle wins; raising the premium
	// weight to 1.0 lets the subtraction beat the load gap and premium wins — proving the weights actually take effect.
	cands := []Candidate{
		{Node: storage.Node{Name: "idle", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 100}, Conns: 20, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "premium-busy", LineType: "iplc", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 100}, Conns: 60, Healthy: true, CostKnown: true, CostFresh: true},
	}
	def := DefaultPolicySetting().Weights()
	idx, ok := Pick(PolicyBalanced, "in", MissingCostDeprioritize, def, cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("缺省应 idle 胜: idx=%d ok=%v", idx, ok)
	}
	strong := ScoreWeights{Load: 0.5, Cost: 0.3, Premium: 1.0}
	idx, ok = Pick(PolicyBalanced, "in", MissingCostDeprioritize, strong, cands)
	if !ok || cands[idx].Node.Name != "premium-busy" {
		t.Fatalf("优质线权重拉高应 premium 胜: idx=%d ok=%v", idx, ok)
	}

	// Cost weight zeroed → pure load ratio: the low-cost but high-load node no longer gets the cost advantage.
	costly := []Candidate{
		{Node: storage.Node{Name: "cheap-busy", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 1}, Conns: 90, Healthy: true, CostKnown: true, CostFresh: true},
		{Node: storage.Node{Name: "pricey-idle", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 500}, Conns: 10, Healthy: true, CostKnown: true, CostFresh: true},
	}
	idx, ok = Pick(PolicyBalanced, "out", MissingCostDeprioritize, ScoreWeights{Load: 1, Cost: 0, Premium: 0}, costly)
	if !ok || costly[idx].Node.Name != "pricey-idle" {
		t.Fatalf("成本权重归零应纯按负载比选 idle: idx=%d ok=%v", idx, ok)
	}

	// normalize: negative and all-zero weights fall back to the default.
	if got := (PolicySetting{WLoad: -1}).Weights(); got != (ScoreWeights{DefaultWLoad, DefaultWCost, DefaultWPremium}) {
		t.Fatalf("负系数应回缺省: %+v", got)
	}
	if got := (PolicySetting{}).Weights(); got != (ScoreWeights{DefaultWLoad, DefaultWCost, DefaultWPremium}) {
		t.Fatalf("全零应回缺省: %+v", got)
	}
}

// TestSavePolicyWeights weight persistence and rejection of invalid values.
func TestSavePolicyWeights(t *testing.T) {
	db := newTestDB(t)
	s := DefaultPolicySetting()
	s.WLoad, s.WCost, s.WPremium = 0.6, 0.25, 0.15
	if err := SavePolicy(db, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPolicy(db)
	if err != nil || got.WLoad != 0.6 || got.WCost != 0.25 || got.WPremium != 0.15 {
		t.Fatalf("weights roundtrip = %+v err=%v", got, err)
	}
	bad := DefaultPolicySetting()
	bad.WLoad = -0.1
	if err := SavePolicy(db, bad); err == nil {
		t.Fatal("负系数必须拒绝")
	}
	// All-zero = follow the default; after storing, the default values read back.
	zero := DefaultPolicySetting()
	zero.WLoad, zero.WCost, zero.WPremium = 0, 0, 0
	if err := SavePolicy(db, zero); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadPolicy(db); got.WLoad != DefaultWLoad {
		t.Fatalf("全零应落缺省: %+v", got)
	}
}

// TestCapacityMbpsFallbacks capacity selection model: measured calibrated > plan downlink > uplink > 100 floor.
func TestCapacityMbpsFallbacks(t *testing.T) {
	cases := []struct {
		name string
		node storage.Node
		want int
	}{
		{"measured wins", storage.Node{SpeedMeasuredMbps: 50, BwDownMbps: 100}, 50},
		{"bwdown next", storage.Node{BwDownMbps: 100, BwUpMbps: 30}, 100},
		{"bwup fallback", storage.Node{BwUpMbps: 30}, 30},
		{"default floor", storage.Node{}, 100},
	}
	for _, tc := range cases {
		if got := (Candidate{Node: tc.node}).CapacityMbps(); got != tc.want {
			t.Fatalf("%s: capacity = %d; want %d", tc.name, got, tc.want)
		}
	}
}

// TestShouldSwitchBalanced the peak-time balanced decision: the combined score must be clearly better (0.15 gap)
// to move; the premium subtraction can flip the decision.
func TestShouldSwitchBalanced(t *testing.T) {
	// Three candidates stretch the normalization range: cur=1.0, win=0.75, gap 0.125 < 0.15, no move.
	mid := Candidate{Node: storage.Node{Name: "mid", BwDownMbps: 100}, Conns: 60, Healthy: true}
	cur := Candidate{Node: storage.Node{Name: "cur", BwDownMbps: 100}, Conns: 100, Healthy: true}
	win := Candidate{Node: storage.Node{Name: "win", BwDownMbps: 100}, Conns: 90, Healthy: true}
	cands := []Candidate{mid, cur, win}
	w := DefaultPolicySetting().Weights()
	if shouldSwitch(PolicyBalanced, "out", MissingCostDeprioritize, w, cands, 1, 2) {
		t.Fatal("0.125 score gap must not switch")
	}
	// win is a premium line (inbound): after subtracting 0.2 the gap is 0.325 > 0.15, move.
	prem := Candidate{Node: storage.Node{Name: "prem", BwDownMbps: 100, LineType: "cn2_gia"}, Conns: 90, Healthy: true}
	cands = []Candidate{mid, cur, prem}
	if !shouldSwitch(PolicyBalanced, "in", MissingCostDeprioritize, w, cands, 1, 2) {
		t.Fatal("premium discount must flip decision")
	}
}

// TestLoopSweepsHooksAndStops Loop sweeps periodically: allocates on start, calls back every round
// (even empty rounds), and returns after cancel.
func TestLoopSweepsHooksAndStops(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	seedNode(t, db, "land", "landing", "out", nil)

	logger := log.New(io.Discard, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan []SwitchEvent, 128)
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, logger, func(evs []SwitchEvent) {
			select {
			case calls <- evs:
			default:
			}
		})
		close(done)
	}()

	deadline := time.After(3 * time.Second)
	var first []SwitchEvent
	for first == nil {
		select {
		case evs := <-calls:
			if len(evs) > 0 {
				first = evs
			}
		case <-deadline:
			t.Fatal("no switch event within deadline")
		}
	}
	if first[0].ToName != "land" {
		t.Fatalf("event = %+v", first[0])
	}
	// Steady-state empty rounds still call back (anti-churn = empty list); wait one more round to prove the period keeps ticking.
	select {
	case <-calls:
	case <-deadline:
		t.Fatal("loop stopped ticking")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not stop on cancel")
	}
}

// TestLoopAnnounceWebhookFailure the notification channel points at an unreachable webhook: a failed send
// only logs and does not block the allocation callbacks.
func TestLoopAnnounceWebhookFailure(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	seedNode(t, db, "land", "landing", "out", nil)
	// Address of a closed server: connections are refused immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u := srv.URL
	srv.Close()
	if err := storage.SetSetting(db, notify.KeyURL, u); err != nil {
		t.Fatal(err)
	}

	logger := log.New(io.Discard, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, 20*time.Millisecond, logger, func(evs []SwitchEvent) {
			if len(evs) > 0 {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		})
		close(done)
	}()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("no switch event")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("loop did not stop")
	}
}

// TestSweepDanglingCurrentLandingName the current landing node has vanished from the candidates (deleted):
// the switch event's FromName falls back to a "#id" placeholder without panicking. Direction in covers the
// policyFor inbound-tier branch.
func TestSweepDanglingCurrentLandingName(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "entry", "entry", "in", nil)
	land := seedNode(t, db, "land", "landing", "in", nil)
	// The current row points at a nonexistent landing ID (no FK constraint, so it can dangle).
	dangling := land.ID + 999
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &entry.ID, LandingNodeID: dangling,
		Direction: "in", Strategy: PolicyPerfFirst, AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v err=%v", events, err)
	}
	if events[0].FromName != fmt.Sprintf("#%d", dangling) {
		t.Fatalf("from = %q; want #%d", events[0].FromName, dangling)
	}
	if events[0].ToName != "land" || events[0].Direction != "in" {
		t.Fatalf("event = %+v", events[0])
	}
}
