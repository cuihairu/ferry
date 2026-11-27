package alloc

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/billing"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/pathhealth"
	// Aliased: the local alloc-policy identifier shadows the package name.
	policyconstraints "github.com/cuihairu/ferry/server/internal/policy"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Failover rebind executor (F-1031): allocation re-points entry-level
// assignments, but a Path row (entry -> (relay) -> landing) is its own
// config artifact -- when its landing node fails, nothing rewrote it. This
// executor rebinds enabled paths whose landing is failing to the policy's
// best healthy alternative, bumping the config version so the panel syncs
// the change downstream.

// PathRebindEvent is one failover rebind of a Path (F-1031).
type PathRebindEvent struct {
	PathID    uint
	PathName  string
	EntryID   uint
	Direction string
	FromID    uint
	FromName  string
	ToID      uint
	ToName    string
	Reason    string
}

// SwitchEvent renders the rebind as the switch event the hooks and the
// notify channel already consume: the path's entry is the switching entry,
// so landing re-pointing (E-16b) follows a failover too.
func (e PathRebindEvent) SwitchEvent() SwitchEvent {
	return SwitchEvent{
		EntryID:   e.EntryID,
		EntryName: e.PathName, // the path identifies the affected route for the panel
		FromName:  e.FromName,
		ToName:    e.ToName,
		Policy:    "failover",
		Reason:    e.Reason,
	}
}

// AlertSickPaths (F-1823) lands one path_down warning per enabled path whose
// landing is failing (left the schedulable pool, or latest probe sick) into
// the herald outbox; the dedup key lets Herald merge the repeats across
// rounds. It runs beside the rebind pass but BEFORE the freeze check: with
// failover=manual nothing re-points the path, which is exactly when the
// operator must hear about it. Emissions are best effort and never fail the
// sweep.
func AlertSickPaths(db *gorm.DB) {
	var paths []storage.Path
	if err := db.Where("enabled = ?", true).Find(&paths).Error; err != nil {
		return
	}
	if len(paths) == 0 {
		return
	}
	var landings []storage.Node
	if err := db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND status != ?",
		true, false, "landing", "both", "provisioning").Find(&landings).Error; err != nil {
		return
	}
	pool := map[uint]bool{}
	for _, l := range landings {
		pool[l.ID] = true
	}
	sick := LatestSick(db)
	for _, p := range paths {
		reason := ""
		if !pool[p.LandingNodeID] {
			reason = "落地离开可调度池（停用/维护/置备中/删除）"
		} else if sick[p.LandingNodeID] {
			reason = "落地探测持续异常"
		}
		if reason == "" {
			continue
		}
		if _, err := herald.Emit(db, herald.EmitInput{
			Kind: herald.KindPathDown, Severity: herald.SeverityWarning,
			Title:    fmt.Sprintf("路径 %s 不可用", p.Name),
			Body:     reason + "，故障转移将按策略重绑或下轮重试",
			Target:   herald.TargetAdmin,
			DedupKey: fmt.Sprintf("path:%d:path_down", p.ID),
			Meta:     map[string]any{"path_id": p.ID, "landing_node_id": p.LandingNodeID},
		}); err != nil {
			log.Printf("herald emit path_down: path=%d %v", p.ID, err)
		}
	}
}

// AlertDegradedPaths (F-1824) lands one path_degraded warning per enabled
// path whose health degraded WITHOUT the landing failing: an offline or
// pool-suspended entry/relay endpoint while the landing still answers probes.
// Landing-side failures alert as path_down (F-1823) -- a path degraded by
// both causes alerts once, under the more severe fact. Emissions are best
// effort and never fail the sweep.
func AlertDegradedPaths(db *gorm.DB) {
	var paths []storage.Path
	if err := db.Where("enabled = ?", true).Find(&paths).Error; err != nil {
		return
	}
	if len(paths) == 0 {
		return
	}
	states := pathhealth.Evaluate(db, paths, time.Now())
	var landings []storage.Node
	if err := db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND status != ?",
		true, false, "landing", "both", "provisioning").Find(&landings).Error; err != nil {
		return
	}
	pool := map[uint]bool{}
	for _, l := range landings {
		pool[l.ID] = true
	}
	sick := LatestSick(db)
	for _, p := range paths {
		if states[p.ID] != pathhealth.Degraded {
			continue
		}
		if !pool[p.LandingNodeID] || sick[p.LandingNodeID] {
			continue // landing-side failure: path_down's territory
		}
		if _, err := herald.Emit(db, herald.EmitInput{
			Kind: herald.KindPathDegraded, Severity: herald.SeverityWarning,
			Title:    fmt.Sprintf("路径 %s 质量下降", p.Name),
			Body:     "入口或中继端点离线/被池暂停，落地探测仍正常",
			Target:   herald.TargetAdmin,
			DedupKey: fmt.Sprintf("path:%d:path_degraded", p.ID),
			Meta:     map[string]any{"path_id": p.ID, "entry_node_id": p.EntryNodeID},
		}); err != nil {
			log.Printf("herald emit path_degraded: path=%d %v", p.ID, err)
		}
	}
}

// RebindSickPaths rewrites enabled Paths whose landing node is failing --
// latest probe verdict sick within the window, or the landing no longer an
// eligible pool member (disabled/maintenance/provisioning/deleted) -- to the
// best healthy alternative under the direction's policy. The version guard
// makes a concurrent panel edit win: a stale version simply no-ops. Returns
// one event per actual rebind; a frozen failover policy (failover=manual,
// F-0294) skips the whole pass.
func RebindSickPaths(db *gorm.DB, now time.Time) ([]PathRebindEvent, error) {
	eff, _, err := policyconstraints.Resolve(db, 0, 0)
	if err != nil {
		return nil, err
	}
	if policyconstraints.FreezeFailover(eff) {
		return nil, nil
	}
	policy, err := LoadPolicy(db)
	if err != nil {
		return nil, err
	}
	var paths []storage.Path
	if err := db.Where("enabled = ?", true).Find(&paths).Error; err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	var landings []storage.Node
	if err := db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND status != ?",
		true, false, "landing", "both", "provisioning").Find(&landings).Error; err != nil {
		return nil, err
	}
	pool := map[uint]storage.Node{}
	for _, l := range landings {
		pool[l.ID] = l
	}
	sick := LatestSick(db)
	conns := LatestConns(db)

	var events []PathRebindEvent
	for _, p := range paths {
		// A failover decision needs the path's own direction: paths are
		// validated out/in at creation, so anything else leaves the row alone.
		direction := p.Direction
		if direction != agentproto.DirectionIn && direction != agentproto.DirectionOut {
			continue
		}
		failed := ""
		if _, ok := pool[p.LandingNodeID]; !ok {
			failed = "landing left the schedulable pool"
		} else if sick[p.LandingNodeID] {
			failed = "landing probe sick"
		}
		if failed == "" {
			continue
		}
		// Alternatives: healthy, direction-matched, capable landings other
		// than the failing one; a sick alternative would just fail over again.
		cands := make([]Candidate, 0, len(landings))
		for _, l := range landings {
			if l.ID == p.LandingNodeID || l.ID == p.EntryNodeID {
				continue
			}
			if l.Direction != direction && l.Direction != agentproto.DirectionBoth {
				continue
			}
			if !storage.HasCapability(l, storage.CapRelayLanding) || sick[l.ID] {
				continue
			}
			known := l.BillingType != "按流量" || l.TrafficPriceCents > 0
			cands = append(cands, Candidate{Node: l, Conns: conns[l.ID], Healthy: true,
				CostKnown: known, CostFresh: now.Sub(l.UpdatedAt) < billing.PriceFreshWindow})
		}
		idx, ok := Pick(policyFor(policy, direction), direction, policy.MissingCost, policy.Weights(), cands)
		if !ok {
			continue // no healthy alternative: leave the path as-is, retry next round
		}
		alt := cands[idx]
		res := db.Model(&storage.Path{}).
			Where("id = ? AND version = ?", p.ID, p.Version).
			Updates(map[string]any{"landing_node_id": alt.Node.ID, "version": p.Version + 1})
		if res.Error != nil {
			return events, res.Error
		}
		if res.RowsAffected == 0 {
			continue // the panel edited the path mid-round; its version wins
		}
		events = append(events, PathRebindEvent{
			PathID: p.ID, PathName: p.Name, EntryID: p.EntryNodeID, Direction: direction,
			FromID: p.LandingNodeID, FromName: nodeName(landings, p.LandingNodeID),
			ToID: alt.Node.ID, ToName: alt.Node.Name,
			Reason: fmt.Sprintf("failover：%s，%s 负载 %d 连/%dM", failed, policyFor(policy, direction), alt.Conns, alt.CapacityMbps()),
		})
	}
	if len(events) > 0 {
		// Decision snapshot (F-1032): a rebind round records itself under the
		// same table as the alloc side, kind "failover". Best-effort: a
		// recording failure must not fail the rebinds already written.
		_ = recordFailoverSnapshot(db, now, policy, len(paths), len(landings), events)
	}
	return events, nil
}

// recordFailoverSnapshot writes the failover round's snapshot (F-1032): the
// same compact replay rows as the alloc side, sourced from the rebind events.
func recordFailoverSnapshot(db *gorm.DB, now time.Time, policy PolicySetting, paths, landings int, events []PathRebindEvent) error {
	rows := make([]decisionSnapshotRow, 0, len(events))
	for _, ev := range events {
		rows = append(rows, decisionSnapshotRow{
			EntryID: ev.EntryID, EntryName: ev.PathName, Direction: ev.Direction,
			Policy: "failover", Action: ActionSwitch,
			From: ev.FromName, To: ev.ToName, Reason: ev.Reason,
		})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	row := storage.AllocDecision{
		Kind: SnapshotKindFailover, RoundAt: now,
		PolicyOut: policy.Out, PolicyIn: policy.In,
		Entries: paths, Landings: landings,
		Switches: len(events), Decisions: string(raw),
	}
	return db.Create(&row).Error
}
