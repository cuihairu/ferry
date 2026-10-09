package alloc

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// newTestDB 建内存库并迁移。
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

// seedNode 落一个节点；mutate 在落库前改字段（容量/成本/线档等打分输入）。
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

// seedProbe 记一条互探结论（新鲜）。
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

// seedConns 记一条节点连接数采样（新鲜）。
func seedConns(t *testing.T, db *gorm.DB, node uint, proc string, conns int) {
	t.Helper()
	if err := db.Create(&storage.NodeTrafficLog{
		NodeID: node, Proc: proc, Conns: conns, RecordedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPickLeastConnCapacityWeight(t *testing.T) {
	// 同连接数下 3M 小带宽负载比高，新连接分给大带宽（容量权重，3M 少分）。
	cands := []Candidate{
		{Node: storage.Node{Name: "small", BwDownMbps: 3}, Conns: 12, Healthy: true},
		{Node: storage.Node{Name: "big", BwDownMbps: 100}, Conns: 12, Healthy: true},
	}
	idx, ok := Pick(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "big" {
		t.Fatalf("capacity weight broken: idx=%d ok=%v", idx, ok)
	}

	// 纯负载口径：连接数少者胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 100}, Conns: 50, Healthy: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 100}, Conns: 5, Healthy: true},
	}
	idx, ok = Pick(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("least conn broken: idx=%d ok=%v", idx, ok)
	}
}

func TestPickCostFirst(t *testing.T) {
	// 包月边际成本 0，压过一切按流量档（月固定成本是沉没成本不参与分配）。
	cands := []Candidate{
		{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 30}, Conns: 10, Healthy: true},
		{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Conns: 0, Healthy: true},
		{Node: storage.Node{Name: "monthly", BillingType: "包月"}, Conns: 20, Healthy: true},
	}
	idx, ok := Pick(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "monthly" {
		t.Fatalf("monthly should win cost_first: idx=%d ok=%v", idx, ok)
	}

	cands = []Candidate{
		{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 30}, Healthy: true},
		{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Healthy: true},
	}
	idx, ok = Pick(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "cheap" {
		t.Fatalf("cheap should win cost_first: idx=%d ok=%v", idx, ok)
	}
}

func TestPickPerfFirstInOnly(t *testing.T) {
	// 回国线：优质线档优先，负载更高也胜。
	cands := []Candidate{
		{Node: storage.Node{Name: "plain-busy", LineType: "163", BwDownMbps: 100}, Conns: 80, Healthy: true},
		{Node: storage.Node{Name: "gia-busy", LineType: "cn2_gia", BwDownMbps: 100}, Conns: 80, Healthy: true},
	}
	idx, ok := Pick(PolicyPerfFirst, "in", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "gia-busy" {
		t.Fatalf("premium line should win perf_first(in): idx=%d ok=%v", idx, ok)
	}

	// 出海线不分线档：负载低者胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "plain-busy", LineType: "163", BwDownMbps: 100}, Conns: 80, Healthy: true},
		{Node: storage.Node{Name: "gia-idle", LineType: "cn2_gia", BwDownMbps: 100}, Conns: 8, Healthy: true},
	}
	idx, ok = Pick(PolicyPerfFirst, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "gia-idle" {
		t.Fatalf("out direction should ignore line tier: idx=%d ok=%v", idx, ok)
	}
}

func TestPickBalanced(t *testing.T) {
	// 均衡档：负载与成本合成，回国优质线作减项。
	cands := []Candidate{
		{Node: storage.Node{Name: "cheap-busy", BwDownMbps: 100}, Conns: 90, Healthy: true},
		{Node: storage.Node{Name: "iplc-loaded", LineType: "iplc", BwDownMbps: 100}, Conns: 60, Healthy: true},
	}
	idx, ok := Pick(PolicyBalanced, "in", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "iplc-loaded" {
		t.Fatalf("premium should win balanced(in): idx=%d ok=%v", idx, ok)
	}

	// 无优质线时低负载胜。
	cands = []Candidate{
		{Node: storage.Node{Name: "busy", BwDownMbps: 100}, Conns: 90, Healthy: true},
		{Node: storage.Node{Name: "idle", BwDownMbps: 100}, Conns: 10, Healthy: true},
	}
	idx, ok = Pick(PolicyBalanced, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("idle should win balanced: idx=%d ok=%v", idx, ok)
	}
}

func TestPickHealthGate(t *testing.T) {
	// 全病不分配；病者出局，健康者按策略比较。
	cands := []Candidate{{Node: storage.Node{Name: "sick"}, Healthy: false}}
	if _, ok := Pick(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands); ok {
		t.Fatal("all-sick candidates must not allocate")
	}
	cands = append(cands, Candidate{Node: storage.Node{Name: "well", BwDownMbps: 100}, Conns: 30, Healthy: true})
	idx, ok := Pick(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands)
	if !ok || cands[idx].Node.Name != "well" {
		t.Fatalf("sick must be excluded: idx=%d ok=%v", idx, ok)
	}
}

func TestPolicyPersistence(t *testing.T) {
	db := newTestDB(t)
	setting, err := LoadPolicy(db)
	if err != nil || setting != DefaultPolicySetting() {
		t.Fatalf("default policy = %+v err=%v", setting, err)
	}

	setting = PolicySetting{Out: PolicyCostFirst, In: PolicyPerfFirst, RebalanceStart: 2, RebalanceEnd: 6}
	setting.WLoad, setting.WCost, setting.WPremium = DefaultWLoad, DefaultWCost, DefaultWPremium
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPolicy(db)
	if err != nil || got != setting {
		t.Fatalf("roundtrip = %+v err=%v", got, err)
	}

	if err := SavePolicy(db, PolicySetting{Out: "greedy", In: PolicyBalanced, RebalanceStart: 1, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid policy must be rejected")
	}
	if err := SavePolicy(db, PolicySetting{Out: PolicyBalanced, In: PolicyBalanced, RebalanceStart: 7, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid window must be rejected")
	}
}

// TestDirectionalDefaults 覆盖方向分流缺省档（E-30）：未设置时出海走性价比
// （cost_first）、回国优先优质线路（perf_first）；空档位保存表示回方向缺省。
func TestDirectionalDefaults(t *testing.T) {
	db := newTestDB(t)
	def := DefaultPolicySetting()
	if def.Out != PolicyCostFirst || def.In != PolicyPerfFirst {
		t.Fatalf("directional defaults = %+v", def)
	}

	if err := SavePolicy(db, PolicySetting{Out: PolicyLeastConn, In: PolicyLeastConn, RebalanceStart: 1, RebalanceEnd: 7}); err != nil {
		t.Fatal(err)
	}
	// 两方向空档位：各自回缺省，窗口保留。
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

	// 非法档位仍拒绝（空≠任意值）。
	if err := SavePolicy(db, PolicySetting{Out: "greedy", In: "", RebalanceStart: 1, RebalanceEnd: 7}); err == nil {
		t.Fatal("invalid out must be rejected")
	}
}

func TestSweepAllocatesAndSwitches(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "entry", "entry", "out", nil)
	seedNode(t, db, "land-a", "landing", "out", nil)
	seedNode(t, db, "land-b", "landing", "out", nil)

	// 首轮：零负载零成本全同，名字字典序 land-a 胜。
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ToName != "land-a" || events[0].FromName != "（无）" {
		t.Fatalf("first sweep events = %+v", events)
	}

	// 无变化复跑不产生新行（不抖动）。
	if events, err = Sweep(db); err != nil || len(events) != 0 {
		t.Fatalf("no-churn sweep = %+v err=%v", events, err)
	}

	// land-a 病了 → 自动换线到 land-b，旧行释放留痕。
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

	// land-a 3M 小带宽吃了 30 条连接（负载比 10），land-b 100M 吃 30 条（0.3）：
	// least_conn 下新入口分给 land-b（3M 少分）。
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

	// 入口级手动行挡住自动。
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

	// 区域级手动行同样挡。
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

	// 摘除态入口不参与分配。
	if err := db.Model(&storage.Node{}).Where("id = ?", entry.ID).
		Update("pool_state", "suspended").Error; err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil || len(events) != 0 {
		t.Fatalf("suspended entry must be skipped: %+v err=%v", events, err)
	}

	// 禁用落地不进候选池：复位入口后唯一落地被禁 → 无候选不动。
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

	// 落地转病：保现行不换线，无新事件无新行。
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
	// least_conn 峰时：主指标省 30% 以上才动。
	cur := Candidate{Node: storage.Node{Name: "cur", BwDownMbps: 100}, Conns: 100, Healthy: true}
	slightly := Candidate{Node: storage.Node{Name: "slightly", BwDownMbps: 100}, Conns: 90, Healthy: true}
	much := Candidate{Node: storage.Node{Name: "much", BwDownMbps: 100}, Conns: 50, Healthy: true}
	cands := []Candidate{cur, slightly, much}
	if shouldSwitch(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("10% better must not switch on peak")
	}
	if !shouldSwitch(PolicyLeastConn, "out", DefaultPolicySetting().Weights(), cands, 0, 2) {
		t.Fatal("50% better must switch on peak")
	}

	// cost_first 峰时：边际成本降 30% 以上才动；包月零成本现行不换。
	cheap := Candidate{Node: storage.Node{Name: "cheap", BillingType: "按流量", TrafficPriceCents: 90}, Healthy: true}
	muchCheap := Candidate{Node: storage.Node{Name: "much-cheap", BillingType: "按流量", TrafficPriceCents: 30}, Healthy: true}
	pricey := Candidate{Node: storage.Node{Name: "pricey", BillingType: "按流量", TrafficPriceCents: 100}, Healthy: true}
	free := Candidate{Node: storage.Node{Name: "free", BillingType: "包月"}, Healthy: true}
	cands = []Candidate{pricey, cheap, muchCheap, free}
	if shouldSwitch(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("10% cheaper must not switch on peak")
	}
	if !shouldSwitch(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), cands, 0, 2) {
		t.Fatal("70% cheaper must switch on peak")
	}
	if !shouldSwitch(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), cands, 0, 3) {
		t.Fatal("monthly free must switch on peak")
	}
	if shouldSwitch(PolicyCostFirst, "out", DefaultPolicySetting().Weights(), []Candidate{free, pricey}, 0, 1) {
		t.Fatal("free current must never switch on peak")
	}

	// perf_first 峰时：只许升档（普线→优质线），不许降级。
	plain := Candidate{Node: storage.Node{Name: "plain", LineType: "163"}, Healthy: true}
	gia := Candidate{Node: storage.Node{Name: "gia", LineType: "cn2_gia"}, Healthy: true}
	cands = []Candidate{plain, gia}
	if !shouldSwitch(PolicyPerfFirst, "in", DefaultPolicySetting().Weights(), cands, 0, 1) {
		t.Fatal("line upgrade must switch on peak")
	}
	if shouldSwitch(PolicyPerfFirst, "in", DefaultPolicySetting().Weights(), []Candidate{gia, plain}, 0, 1) {
		t.Fatal("line downgrade must not switch on peak")
	}
}

func TestSweepPeakHysteresisAndOffpeakRebalance(t *testing.T) {
	db := newTestDB(t)
	entry := seedNode(t, db, "entry", "entry", "out", nil)
	landA := seedNode(t, db, "land-a", "landing", "out", nil)
	landB := seedNode(t, db, "land-b", "landing", "out", nil)

	// 现行 land-a（100M 兜底）。land-b 同容量但连接略少：峰时 10% 优势不动，
	// 低峰窗口全序更优即再平衡。
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

	// 峰时（12 点）：land-b 仅优 10%，不换。
	events, err := SweepAt(db, time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local))
	if err != nil || len(events) != 0 {
		t.Fatalf("peak slight gain must hold: %+v err=%v", events, err)
	}

	// 低峰（3 点）：全序更优即再平衡。
	events, err = SweepAt(db, time.Date(2026, 10, 7, 3, 0, 0, 0, time.Local))
	if err != nil || len(events) != 1 || events[0].ToName != "land-b" {
		t.Fatalf("offpeak rebalance = %+v err=%v", events, err)
	}

	// 峰时大幅更优：land-c 负载比 0.3 vs 现行 0.9，显著更优即动。
	landC := seedNode(t, db, "land-c", "landing", "out", nil)
	seedConns(t, db, landC.ID, "relay", 30)
	events, err = SweepAt(db, time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local))
	if err != nil || len(events) != 1 || events[0].ToName != "land-c" {
		t.Fatalf("peak big gain must switch: %+v err=%v", events, err)
	}
}

// TestPickBalancedWeights 系数 dash 可配（调度核心设计 §权重合成分）：
// 缺省 0.5/0.3/0.2 复现现公式；改系数即改结果——成本权重拉高时低成本
// 胜出，成本权重归零时退化为纯负载比，负系数/全零回缺省。
func TestPickBalancedWeights(t *testing.T) {
	// cheap-idle（低负载低零成本）对 busy-premium（高负载优质线低值减项）：
	// 缺省下优质线减项 0.2 不足以覆盖负载差，idle 胜；把 premium 权重提到
	// 1.0 后减项压过负载差，premium 胜——证明系数真实生效。
	cands := []Candidate{
		{Node: storage.Node{Name: "idle", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 100}, Conns: 20, Healthy: true},
		{Node: storage.Node{Name: "premium-busy", LineType: "iplc", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 100}, Conns: 60, Healthy: true},
	}
	def := DefaultPolicySetting().Weights()
	idx, ok := Pick(PolicyBalanced, "in", def, cands)
	if !ok || cands[idx].Node.Name != "idle" {
		t.Fatalf("缺省应 idle 胜: idx=%d ok=%v", idx, ok)
	}
	strong := ScoreWeights{Load: 0.5, Cost: 0.3, Premium: 1.0}
	idx, ok = Pick(PolicyBalanced, "in", strong, cands)
	if !ok || cands[idx].Node.Name != "premium-busy" {
		t.Fatalf("优质线权重拉高应 premium 胜: idx=%d ok=%v", idx, ok)
	}

	// 成本权重归零 → 纯负载比：低成本但高负载者不再受成本优待。
	costly := []Candidate{
		{Node: storage.Node{Name: "cheap-busy", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 1}, Conns: 90, Healthy: true},
		{Node: storage.Node{Name: "pricey-idle", BwDownMbps: 100, BillingType: "按流量", TrafficPriceCents: 500}, Conns: 10, Healthy: true},
	}
	idx, ok = Pick(PolicyBalanced, "out", ScoreWeights{Load: 1, Cost: 0, Premium: 0}, costly)
	if !ok || costly[idx].Node.Name != "pricey-idle" {
		t.Fatalf("成本权重归零应纯按负载比选 idle: idx=%d ok=%v", idx, ok)
	}

	// normalize：负系数与全零回缺省。
	if got := (PolicySetting{WLoad: -1}).Weights(); got != (ScoreWeights{DefaultWLoad, DefaultWCost, DefaultWPremium}) {
		t.Fatalf("负系数应回缺省: %+v", got)
	}
	if got := (PolicySetting{}).Weights(); got != (ScoreWeights{DefaultWLoad, DefaultWCost, DefaultWPremium}) {
		t.Fatalf("全零应回缺省: %+v", got)
	}
}

// TestSavePolicyWeights 系数持久化与非法拒绝。
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
	// 全零=按缺省，落库后读回缺省值。
	zero := DefaultPolicySetting()
	zero.WLoad, zero.WCost, zero.WPremium = 0, 0, 0
	if err := SavePolicy(db, zero); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadPolicy(db); got.WLoad != DefaultWLoad {
		t.Fatalf("全零应落缺省: %+v", got)
	}
}
