package alloc

import (
	"fmt"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/billing"
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
	PathID   uint
	PathName string
	EntryID  uint
	FromID   uint
	FromName string
	ToID     uint
	ToName   string
	Reason   string
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
			PathID: p.ID, PathName: p.Name, EntryID: p.EntryNodeID,
			FromID: p.LandingNodeID, FromName: nodeName(landings, p.LandingNodeID),
			ToID: alt.Node.ID, ToName: alt.Node.Name,
			Reason: fmt.Sprintf("failover：%s，%s 负载 %d 连/%dM", failed, policyFor(policy, direction), alt.Conns, alt.CapacityMbps()),
		})
	}
	return events, nil
}
