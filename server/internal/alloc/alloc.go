// Package alloc auto-assigns landings (E-21): weighted least-connections — pick a landing from the landing pool
// for each entry per the configurable policy, with a trail left in landing_assignments (strategy != manual).
// Manual assignments win: entries with an effective manual row (entry-level or their region-level) are excluded from auto.
// Signals: connection counts come from the latest node traffic report sample (A-20), health from the latest probe verdict
// within the window, capacity = measured calibrated bandwidth > plan bandwidth, and cost counts only metered nodes' marginal traffic price.
package alloc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/notify"
	// Aliased: the local alloc-policy identifier (PolicySetting values and
	// policy params) shadows the package name everywhere below.
	policyconstraints "github.com/cuihairu/ferry/server/internal/policy"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Allocation policy values (aligned with LandingAssignment.Strategy; manual is excluded from auto).
const (
	PolicyLeastConn = "least_conn"
	PolicyCostFirst = "cost_first"
	PolicyPerfFirst = "perf_first"
	PolicyBalanced  = "balanced"
	PolicyManual    = "manual"
)

// settingKey is the policy persistence key (panel KV); the value is JSON: one tier per direction.
const settingKey = "alloc_policy"

// Evaluation model defaults.
const (
	// DefaultPolicyOut/In are the per-direction default tiers (E-30): outbound favors cost-effectiveness (cost first),
	// inbound favors premium lines (perf first, higher cost tolerance — an uncongested evening peak beats saving a few cents).
	DefaultPolicyOut = PolicyCostFirst
	DefaultPolicyIn  = PolicyPerfFirst
	// ProbeFresh is how long a probe verdict participates in the health decision; older counts as no verdict (treated as healthy).
	ProbeFresh = 30 * time.Minute
	// LoadFresh is how long a connection sample participates in the load decision; older counts as no sample (treated as 0 connections).
	LoadFresh = 10 * time.Minute
)

// ValidPolicy reports whether the policy value is valid.
func ValidPolicy(p string) bool {
	switch p {
	case PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced:
		return true
	}
	return false
}

// PolicySetting holds one tier per direction: outbound and inbound pools weight differently (design doc §C.5.2);
// rebalancing happens in the off-peak window (hours, [Start,End) half-open) — a switch requires being strictly better
// in the policy total order; switching during peak is conservative to prevent trail churn (E-22).
type PolicySetting struct {
	Out string `json:"out"`
	In  string `json:"in"`
	// RebalanceStart/RebalanceEnd are the off-peak window hours (0-23), default 1-7.
	RebalanceStart int `json:"rebalance_start"`
	RebalanceEnd   int `json:"rebalance_end"`
	// The balanced policy's multiplicative score weights (scheduler core design §weight composition): score =
	// WLoad*normalized load + WCost*normalized cost − WPremium*premium line (inbound lines only). Each weight is
	// dash-editable; the defaults 0.5/0.3/0.2 reproduce the current four-policy formula, behavior changes only when reconfigured.
	WLoad    float64 `json:"w_load"`
	WCost    float64 `json:"w_cost"`
	WPremium float64 `json:"w_premium"`
	// Cost guards (F-0604~F-0606): MaxCostPerGB (0 = off) excludes metered
	// candidates whose known fresh marginal price exceeds it; MissingCost picks
	// the degradation rule for an unknown/stale price — deprioritize (default,
	// F-0605/F-0610: never treated as free) or treat_zero (legacy behavior).
	MaxCostPerGB int64  `json:"max_cost_per_gb"`
	MissingCost  string `json:"missing_cost"`
}

// Multiplicative score weight defaults (= the current formula 0.5*loadN + 0.3*costN - 0.2*premium).
const (
	DefaultWLoad    = 0.5
	DefaultWCost    = 0.3
	DefaultWPremium = 0.2
)

// MissingCost degradation rules (F-0605/F-0610): how a metered candidate with an
// unknown or stale price participates in the cost decision.
const (
	// MissingCostDeprioritize sorts missing-price candidates after every priced
	// one (an unknown price is never treated as free); this is the default.
	MissingCostDeprioritize = "deprioritize"
	// MissingCostTreatZero keeps the legacy behavior: a missing price enters the
	// cost decision as 0 (free).
	MissingCostTreatZero = "treat_zero"
)

// DefaultMissingCost is the degradation rule for settings that do not carry one
// (including all settings persisted before the field existed).
const DefaultMissingCost = MissingCostDeprioritize

// ValidMissingCost reports whether the degradation rule value is valid.
func ValidMissingCost(m string) bool {
	return m == MissingCostDeprioritize || m == MissingCostTreatZero
}

// Off-peak window defaults.
const (
	DefaultRebalanceStart = 1
	DefaultRebalanceEnd   = 7
)

// DefaultPolicySetting takes the default tier for both directions.
func DefaultPolicySetting() PolicySetting {
	return PolicySetting{
		Out: DefaultPolicyOut, In: DefaultPolicyIn,
		RebalanceStart: DefaultRebalanceStart, RebalanceEnd: DefaultRebalanceEnd,
		WLoad: DefaultWLoad, WCost: DefaultWCost, WPremium: DefaultWPremium,
		MissingCost: DefaultMissingCost,
	}
}

// LoadPolicy reads the policy setting; unset falls back to the default.
func LoadPolicy(db *gorm.DB) (PolicySetting, error) {
	setting := DefaultPolicySetting()
	raw, ok, err := storage.GetSetting(db, settingKey)
	if err != nil || !ok {
		return setting, err
	}
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		return DefaultPolicySetting(), nil // corrupt data follows the default without blocking allocation
	}
	setting.normalize()
	return setting, nil
}

// ScoreWeights are the balanced policy's multiplicative score weights (each weight dash-editable).
type ScoreWeights struct {
	Load    float64
	Cost    float64
	Premium float64
}

// Weights returns the weights; default/invalid values fall back to the current four-policy formula.
func (s PolicySetting) Weights() ScoreWeights {
	s.normalize()
	return ScoreWeights{Load: s.WLoad, Cost: s.WCost, Premium: s.WPremium}
}

// normalize fixes invalid fields back to the default tier.
func (s *PolicySetting) normalize() {
	if !ValidPolicy(s.Out) {
		s.Out = DefaultPolicyOut
	}
	if !ValidPolicy(s.In) {
		s.In = DefaultPolicyIn
	}
	if !validWindow(s.RebalanceStart, s.RebalanceEnd) {
		s.RebalanceStart, s.RebalanceEnd = DefaultRebalanceStart, DefaultRebalanceEnd
	}
	// Weights: default/non-positive values fall back to the default (weights are non-negative and not all-zero, avoiding division by zero and the all-zero degenerate case).
	if s.WLoad < 0 || s.WCost < 0 || s.WPremium < 0 ||
		(s.WLoad == 0 && s.WCost == 0 && s.WPremium == 0) {
		s.WLoad, s.WCost, s.WPremium = DefaultWLoad, DefaultWCost, DefaultWPremium
	}
	// Cost guards (F-0604~F-0606): a negative ceiling means off; an empty or
	// invalid degradation rule (settings persisted before the field existed)
	// falls back to deprioritize.
	if s.MaxCostPerGB < 0 {
		s.MaxCostPerGB = 0
	}
	if !ValidMissingCost(s.MissingCost) {
		s.MissingCost = DefaultMissingCost
	}
}

// validWindow defines the off-peak window: 0-23 with start < end (half-open, no crossing midnight).
func validWindow(start, end int) bool {
	return start >= 0 && start <= 23 && end >= 0 && end <= 23 && start < end
}

// SavePolicy validates and persists the policy setting.
func SavePolicy(db *gorm.DB, setting PolicySetting) error {
	// An empty tier means "per-direction default" (outbound cost-effective / inbound premium); only invalid tiers are rejected.
	if setting.Out == "" {
		setting.Out = DefaultPolicyOut
	}
	if setting.In == "" {
		setting.In = DefaultPolicyIn
	}
	if !ValidPolicy(setting.Out) || !ValidPolicy(setting.In) {
		return fmt.Errorf("policy must be %s/%s/%s/%s", PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced)
	}
	if !validWindow(setting.RebalanceStart, setting.RebalanceEnd) {
		return fmt.Errorf("rebalance window must be 0-23 with start < end")
	}
	if setting.WLoad < 0 || setting.WCost < 0 || setting.WPremium < 0 {
		return fmt.Errorf("score weights must be non-negative")
	}
	if setting.WLoad == 0 && setting.WCost == 0 && setting.WPremium == 0 {
		// All-zero = follow the default (equivalent to unset), not rejected.
		setting.WLoad, setting.WCost, setting.WPremium = DefaultWLoad, DefaultWCost, DefaultWPremium
	}
	if setting.MaxCostPerGB < 0 {
		return fmt.Errorf("max cost per GB must be non-negative")
	}
	if setting.MissingCost == "" {
		// An empty degradation rule means "use the default", like the empty tiers above.
		setting.MissingCost = DefaultMissingCost
	}
	if !ValidMissingCost(setting.MissingCost) {
		return fmt.Errorf("missing cost must be %s/%s", MissingCostDeprioritize, MissingCostTreatZero)
	}
	raw, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	return storage.SetSetting(db, settingKey, string(raw))
}

// InOffPeak reports whether now falls inside the off-peak rebalance window.
func InOffPeak(now time.Time, s PolicySetting) bool {
	s.normalize()
	h := now.Hour()
	return h >= s.RebalanceStart && h < s.RebalanceEnd
}

// Candidate is the scoring input for one landing candidate.
type Candidate struct {
	Node      storage.Node
	Conns     int  // latest connection count (no sample = 0)
	Healthy   bool // latest probe verdict within the window is not sick (no verdict counts as healthy)
	CostKnown bool // metered price present (fixed billing is always known: marginal 0 is exact by design)
	CostFresh bool // price row fresh within billing.PriceFreshWindow (node updated_at is the price as-of marker)
}

// costMissing reports whether the marginal price is unknown or stale under the
// configured degradation rule (F-0605/F-0610): only metered billing can be
// missing; fixed-cost nodes are never missing because their marginal 0 is exact.
func (c Candidate) costMissing(rule string) bool {
	if rule == MissingCostTreatZero {
		return false
	}
	return c.Node.BillingType == "按流量" && (!c.CostKnown || !c.CostFresh)
}

// CapacityMbps is the forwarding capacity (Mbps): measured calibrated > plan downlink > plan uplink > 100 floor.
// Bandwidth serves as the capacity weight — a 3M small pipe receives fewer new connections by capacity, so it never floods over.
func (c Candidate) CapacityMbps() int {
	switch {
	case c.Node.SpeedMeasuredMbps > 0:
		return c.Node.SpeedMeasuredMbps
	case c.Node.BwDownMbps > 0:
		return c.Node.BwDownMbps
	case c.Node.BwUpMbps > 0:
		return c.Node.BwUpMbps
	}
	return 100
}

// CostPerGB is the marginal traffic cost (cents/GB): only metered billing enters the cost decision;
// monthly/fixed-bandwidth fixed monthly cost is sunk cost and does not affect allocation (design doc §C.5.2 billing model).
func (c Candidate) CostPerGB() int64 {
	if c.Node.BillingType != "按流量" {
		return 0
	}
	return c.Node.TrafficPriceCents
}

// Pick selects the best candidate per the policy and returns its index; nothing allocatable (all sick or empty) returns false.
// Model:
//   - least_conn: lowest load ratio (conns/capacityM), tie broken by lower cost;
//   - cost_first: lowest marginal cost, tie broken by lower load ratio;
//   - perf_first: premium inbound line tiers (cn2_gia/cu_vip/cmi/iplc) first, then load ratio;
//   - balanced: load/cost normalized weights 0.5+0.3, premium inbound line minus 0.2 (direction=in only).
//
// Under the deprioritize rule (F-0605/F-0610) a candidate with an unknown or
// stale metered price sorts after every priced one in every cost comparison,
// instead of entering as free.
//
// Equal scores break by name lexicographic order, keeping the result deterministic and testable.
func Pick(policy, direction, missing string, w ScoreWeights, cands []Candidate) (int, bool) {
	healthy := make([]int, 0, len(cands))
	for i, c := range cands {
		if c.Healthy {
			healthy = append(healthy, i)
		}
	}
	if len(healthy) == 0 {
		return 0, false
	}
	if len(healthy) == 1 {
		return healthy[0], true
	}
	less := LessFor(policy, direction, missing, w, cands)
	best := healthy[0]
	for _, i := range healthy[1:] {
		if less(i, best) {
			best = i
		}
	}
	return best, true
}

// Load metric: load ratio (conns/capacityM) — a 3M small pipe receives fewer connections by capacity.
func loadRatio(c Candidate) float64 {
	return float64(c.Conns) / float64(c.CapacityMbps())
}

// costPer is the marginal traffic cost (cents/GB).
func costPer(c Candidate) float64 {
	return float64(c.CostPerGB())
}

// premiumOf applies the line tier only to inbound lines (design doc §C.5.2); outbound lines have no tier split.
func premiumOf(c Candidate, direction string) bool {
	return direction == agentproto.DirectionIn && isPremiumLine(c.Node.LineType)
}

// LessFor builds the policy comparison function: less(a,b) = whether a beats b.
// Exported for the candidate preview, which renders the same order the sweep picks from.
func LessFor(policy, direction, missingRule string, w ScoreWeights, cands []Candidate) func(a, b int) bool {
	load := func(i int) float64 { return loadRatio(cands[i]) }
	missing := func(i int) bool { return cands[i].costMissing(missingRule) }
	// Missing prices map to 0 inside the per-policy comparator: the wrapper
	// below partitions them behind every priced candidate, so their cost value
	// only breaks ties among themselves.
	cost := func(i int) float64 {
		if missing(i) {
			return 0
		}
		return costPer(cands[i])
	}
	premium := func(i int) bool { return premiumOf(cands[i], direction) }
	byName := func(a, b int) bool { return cands[a].Node.Name < cands[b].Node.Name }
	var less func(a, b int) bool
	switch policy {
	case PolicyCostFirst:
		less = func(a, b int) bool {
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			return byName(a, b)
		}
	case PolicyPerfFirst:
		less = func(a, b int) bool {
			if pa, pb := premium(a), premium(b); pa != pb {
				return pa // premium line wins
			}
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			return byName(a, b)
		}
	case PolicyBalanced:
		// Normalize to [0,1] then weight; a premium line is a fixed subtraction.
		// Cost normalizes over priced candidates only (F-0605/F-0610): a missing
		// price entering the min/max range as 0 would squeeze the priced spread,
		// so it is excluded there; within the missing bucket every cost term is
		// an equal constant and load/premium/name decide.
		all := make([]int, 0, len(cands))
		priced := make([]int, 0, len(cands))
		for i := range cands {
			all = append(all, i)
			if !missing(i) {
				priced = append(priced, i)
			}
		}
		loadN := normalize(cands, all, load)
		costIdx := priced
		if len(costIdx) == 0 {
			costIdx = all
		}
		costN := normalize(cands, costIdx, cost)
		less = func(a, b int) bool {
			sa := w.Load*loadN(a) + w.Cost*costN(a)
			sb := w.Load*loadN(b) + w.Cost*costN(b)
			if premium(a) {
				sa -= w.Premium
			}
			if premium(b) {
				sb -= w.Premium
			}
			if sa != sb {
				return sa < sb
			}
			return byName(a, b)
		}
	default: // least_conn
		less = func(a, b int) bool {
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			return byName(a, b)
		}
	}
	// Partition by the degradation rule (F-0605/F-0610): a missing price never
	// meets a priced one on cost — it loses that comparison outright, and the
	// policy order decides as usual within each bucket. treat_zero keeps the
	// legacy single-bucket behavior.
	if missingRule == MissingCostTreatZero {
		return less
	}
	return func(a, b int) bool {
		if ma, mb := missing(a), missing(b); ma != mb {
			return !ma
		}
		return less(a, b)
	}
}

// Peak switch threshold: the primary metric must be clearly better (saving 30% or more) to move, preventing trail churn (E-22).
const peakSwitchMargin = 0.7

// shouldSwitch is the peak switch decision (the off-peak window switches directly by the policy total order, bypassing this check).
// When the current landing is not among healthy candidates (sick/offline) the caller force-switches and does not reach this function.
func shouldSwitch(policy, direction, missingRule string, w ScoreWeights, cands []Candidate, cur, win int) bool {
	// Missing prices (F-0605/F-0610) for the cost-aware policies: never switch
	// at peak from a known price to an unknown one (an unknown price is not a
	// proven improvement), and leaving an unknown price for a known one is a
	// clear improvement regardless of the margin. least_conn/perf_first do not
	// compare cost here, so the rule does not apply to them.
	curMissing := cands[cur].costMissing(missingRule)
	winMissing := cands[win].costMissing(missingRule)
	switch policy {
	case PolicyPerfFirst:
		// During peak only line-tier upgrades are allowed (plain → premium); downgrades wait for the off-peak rebalance.
		return !premiumOf(cands[cur], direction) && premiumOf(cands[win], direction)
	case PolicyCostFirst:
		if curMissing != winMissing {
			return curMissing
		}
		curCost := costPer(cands[cur])
		return curCost > 0 && costPer(cands[win]) < curCost*peakSwitchMargin
	case PolicyBalanced:
		if curMissing != winMissing {
			return curMissing
		}
		all := make([]int, len(cands))
		priced := make([]int, 0, len(cands))
		for i := range cands {
			all[i] = i
			if !cands[i].costMissing(missingRule) {
				priced = append(priced, i)
			}
		}
		loadN := normalize(cands, all, func(i int) float64 { return loadRatio(cands[i]) })
		costIdx := priced
		if len(costIdx) == 0 {
			costIdx = all
		}
		costN := normalize(cands, costIdx, func(i int) float64 {
			if cands[i].costMissing(missingRule) {
				return 0
			}
			return costPer(cands[i])
		})
		score := func(i int) float64 {
			s := w.Load*loadN(i) + w.Cost*costN(i)
			if premiumOf(cands[i], direction) {
				s -= w.Premium
			}
			return s
		}
		return score(win) < score(cur)-0.15 // combined score clearly better
	default: // least_conn
		curLoad := loadRatio(cands[cur])
		return loadRatio(cands[win]) < curLoad*peakSwitchMargin
	}
}

// isPremiumLine is the premium inbound line tier.
func isPremiumLine(lineType string) bool {
	switch lineType {
	case "cn2_gia", "cu_vip", "cmi", "iplc":
		return true
	}
	return false
}

// normalize min-max normalizes a metric across healthy candidates into [0,1]; all-equal values return a constant 0.5.
func normalize(cands []Candidate, idx []int, metric func(int) float64) func(int) float64 {
	lo, hi := metric(idx[0]), metric(idx[0])
	for _, i := range idx[1:] {
		v := metric(i)
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi == lo {
		return func(int) float64 { return 0.5 }
	}
	return func(i int) float64 { return (metric(i) - lo) / (hi - lo) }
}

// SwitchEvent is one automatic line switch.
type SwitchEvent struct {
	EntryID   uint
	EntryName string
	FromName  string
	ToName    string
	Direction string
	Policy    string
	Reason    string
}

// Loop runs the allocation decision periodically until ctx is canceled; switches are logged and emitted as events.
// Loop runs allocation periodically; hooks receive the event list after each round of switches
// (E-16b: the main flow wires relaypush.OnSwitch for landing re-pointing; tests may omit them).
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger, hooks ...func([]SwitchEvent)) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTimer(0) // run one round on start
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := sweepOnce(db, logger, hooks); err != nil {
			logger.Printf("alloc sweep: %v", err)
		}
		t.Reset(interval)
	}
}

func sweepOnce(db *gorm.DB, logger *log.Logger, hooks []func([]SwitchEvent)) error {
	events, err := Sweep(db)
	if err != nil {
		return err
	}
	for _, ev := range events {
		logger.Printf("alloc switch: entry %d %s %s %s -> %s（%s）",
			ev.EntryID, ev.EntryName, ev.Direction, ev.FromName, ev.ToName, ev.Reason)
		announce(db, logger, ev)
	}
	for _, hook := range hooks {
		hook(events)
	}
	return nil
}

// Sweep runs one allocation round: every allocatable entry (enabled, active in pool, no effective manual
// assignment) picks a landing per its direction's policy; when it differs from the current auto row, the old row is
// released with a trail and a new one created. Peak switching is conservative (only on a clearly better primary metric), off-peak rebalances by the policy total order.
// Returns the switch events. With all candidates sick the entry stays put this round (keep current, wait for recovery).
func Sweep(db *gorm.DB) ([]SwitchEvent, error) {
	return SweepAt(db, time.Now())
}

// SweepAt is Sweep with an injectable clock, for pinning time in tests.
// The round is the unified three-step pipeline (F-1022/F-1023): load the
// input model, decide purely, apply the writes.
func SweepAt(db *gorm.DB, now time.Time) ([]SwitchEvent, error) {
	// Failover freeze (F-0294): a policy chain setting failover=manual stops
	// auto landing switches this round (current assignments are kept).
	eff, _, err := policyconstraints.Resolve(db, 0, 0)
	if err != nil {
		return nil, err
	}
	if policyconstraints.FreezeFailover(eff) {
		return nil, nil
	}
	req, err := BuildRequest(db, now)
	if err != nil {
		return nil, err
	}
	return Apply(db, req, Decide(req))
}

// policyFor returns the tier for the direction.
func policyFor(p PolicySetting, direction string) string {
	if direction == agentproto.DirectionIn {
		return p.In
	}
	return p.Out
}

// hasManualAssignment reports whether an entry-level or region-level manual assignment is effective.
func hasManualAssignment(db *gorm.DB, entryID uint, region string) (bool, error) {
	var n int64
	if err := db.Model(&storage.LandingAssignment{}).
		Where("released_at IS NULL AND strategy = ? AND (entry_node_id = ? OR (entry_node_id IS NULL AND region = ?))",
			PolicyManual, entryID, region).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// currentAuto returns the entry's latest effective auto row for the direction.
func currentAuto(db *gorm.DB, entryID uint, direction string) (*storage.LandingAssignment, error) {
	var row storage.LandingAssignment
	err := db.Where("released_at IS NULL AND strategy != ? AND entry_node_id = ? AND direction = ?",
		PolicyManual, entryID, direction).
		Order("id DESC").First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// exclusionDedupeWindow is how long an identical exclusion trail suppresses a
// repeat row: sweeps run every few minutes, one row per hour per (entry,
// landing, reason) is enough to read the history without flooding it.
const exclusionDedupeWindow = time.Hour

// recordExclusion trails one cost-ceiling exclusion (F-0606) for the audit
// trail; identical rows within the dedupe window are skipped so a repeating
// sweep does not flood the table.
func recordExclusion(db *gorm.DB, entryID, landingID uint, direction, policyName, reason string, now time.Time) error {
	var n int64
	if err := db.Model(&storage.AllocExclusion{}).
		Where("entry_node_id = ? AND landing_node_id = ? AND direction = ? AND policy = ? AND reason = ? AND created_at > ?",
			entryID, landingID, direction, policyName, reason, now.Add(-exclusionDedupeWindow)).
		Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	row := storage.AllocExclusion{
		EntryNodeID:   entryID,
		LandingNodeID: landingID,
		Direction:     direction,
		Policy:        policyName,
		Reason:        reason,
		CreatedAt:     now,
	}
	return db.Create(&row).Error
}

// LatestConns aggregates each node's latest sampled connection count: take the latest entry per process within the window, then sum per node.
func LatestConns(db *gorm.DB) map[uint]int {
	out := map[uint]int{}
	since := time.Now().Add(-LoadFresh)
	var logs []storage.NodeTrafficLog
	if err := db.Where("recorded_at > ?", since).
		Order("recorded_at ASC, id ASC").Find(&logs).Error; err != nil {
		return out // on query failure treat as no sample (load 0), without blocking allocation
	}
	perProc := map[uint]map[string]int{}
	for _, l := range logs {
		if perProc[l.NodeID] == nil {
			perProc[l.NodeID] = map[string]int{}
		}
		perProc[l.NodeID][l.Proc] = l.Conns // last write wins = latest per process
	}
	for nodeID, procs := range perProc {
		total := 0
		for _, c := range procs {
			total += c
		}
		out[nodeID] = total
	}
	return out
}

// LatestSick returns the set of target nodes with a latest sick verdict within the window; not in the set = healthy or no verdict
// (no verdict counts as healthy so cold starts can complete the first allocation).
func LatestSick(db *gorm.DB) map[uint]bool {
	out := map[uint]bool{}
	since := time.Now().Add(-ProbeFresh)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at > ? AND target_kind IN ? AND target_node_id IS NOT NULL",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return out
	}
	for _, r := range reports {
		out[*r.TargetNodeID] = r.Verdict == agentproto.ProbeVerdictSick // last write wins = latest
	}
	return out
}

func nodeName(nodes []storage.Node, id uint) string {
	for _, n := range nodes {
		if n.ID == id {
			return n.Name
		}
	}
	return fmt.Sprintf("#%d", id)
}

// announce emits the auto-switch event (P1-10 notification channel); failures only log and do not block.
func announce(db *gorm.DB, logger *log.Logger, ev SwitchEvent) {
	n := notify.FromDB(db)
	if !n.Enabled() {
		return
	}
	e := notify.Event{
		Event: "alloc.switch",
		Text:  fmt.Sprintf("落地自动换线：%s %s 由 %s 切至 %s（%s）", ev.EntryName, ev.Direction, ev.FromName, ev.ToName, ev.Reason),
		Fields: map[string]any{
			"entry_id": ev.EntryID, "direction": ev.Direction,
			"policy": ev.Policy, "reason": ev.Reason,
		},
	}
	if err := n.Send(e); err != nil {
		logger.Printf("notify webhook: %v", err)
	}
}
