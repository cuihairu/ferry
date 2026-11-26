package alloc

import (
	"fmt"
	"sort"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/billing"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Unified scheduling input/output models (F-1022/F-1023). One sweep round is
// split into three steps so every consumer -- the sweep loop, the panel
// preview, a future simulator -- shares the same model instead of each
// re-querying signals:
//
//	BuildRequest(db, now) -> *Request   load everything one round decides from
//	Decide(req)           -> []Decision pure per-entry evaluation, no writes
//	Apply(db, req, decs)  -> []SwitchEvent  trail exclusions, write switches

// Decision actions.
const (
	// ActionSwitch releases the current auto row and assigns the winner.
	ActionSwitch = "switch"
	// ActionKeep keeps the current landing (already the best, or the peak
	// switch margin is not met).
	ActionKeep = "keep"
	// ActionNone means nothing allocatable this round (all sick or no
	// candidate survived the filters); the entry stays put and waits.
	ActionNone = "none"
	// ActionSkip means the entry itself is not schedulable (e.g. it lacks the
	// relay_entry capability, so no tunnel can come up).
	ActionSkip = "skip"
)

// Request (F-1022) is the unified scheduling input: everything one round
// decides from, loaded once instead of queried per entry, so the decision is
// reproducible from a snapshot and the same value can feed the preview or a
// dry-run.
type Request struct {
	Now      time.Time
	Policy   PolicySetting
	Entries  []storage.Node
	Landings []storage.Node
	// Conns maps node -> latest sampled connection count within LoadFresh.
	Conns map[uint]int
	// Sick maps node -> its latest verdict within ProbeFresh is sick; absent
	// means healthy or no verdict (cold start counts as healthy).
	Sick map[uint]bool
	// Quality maps node -> its fresh probe quality (F-1028); absent means no
	// fresh sample, which scores neutral.
	Quality map[uint]Quality
	// Manual marks entries with an effective manual assignment row; they are
	// excluded from auto entirely.
	Manual map[uint]bool
	// Current maps entry -> its latest effective auto assignment row (for the
	// entry's folded direction).
	Current map[uint]*storage.LandingAssignment
}

// Exclusion is one candidate ruled out before ranking, with the
// operator-readable reason; the executor trails it (with dedupe) so the panel
// can explain why a landing was never considered.
type Exclusion struct {
	LandingID uint
	Reason    string
}

// CandidateEval is one candidate's answer inside a Decision: the scoring
// input plus why it is not eligible (an eligible candidate carries no reason).
type CandidateEval struct {
	Candidate    Candidate
	Eligible     bool
	Reason       string
	QualityScore float64 // the candidate's probe quality folded to [0,1] (F-1028)
}

// Decision (F-1023) is one entry's scheduling answer: the candidates it
// considered in policy order, each with its eligibility, the winner the policy
// picked, and the action to take. A Decision with no healthy candidate still
// carries the ranked list, so the output explains the round instead of only
// recording switches.
type Decision struct {
	EntryID    uint
	EntryName  string
	Direction  string
	Policy     string
	Candidates []CandidateEval
	Exclusions []Exclusion
	WinnerID   uint
	Action     string
	FromID     uint
	ToID       uint
	Reason     string
}

// foldDirection collapses a node direction to the single direction allocation
// decides for: relay is single-process single-direction (one landing
// address), so both entries allocate by the outbound tier and unknown values
// follow outbound.
func foldDirection(d string) string {
	if d == agentproto.DirectionIn {
		return agentproto.DirectionIn
	}
	return agentproto.DirectionOut
}

// BuildRequest loads one round's input model (F-1022): the policy, the
// schedulable entry and landing pools, the load/health signals, and per-entry
// manual flags and current auto rows. Errors abort before any decision.
func BuildRequest(db *gorm.DB, now time.Time) (*Request, error) {
	policy, err := LoadPolicy(db)
	if err != nil {
		return nil, err
	}
	var entries []storage.Node
	if err := db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
		true, false, "entry", "both", "active", "provisioning").Find(&entries).Error; err != nil {
		return nil, err
	}
	// provisioning (OS-3, being provisioned) does not join allocation; once online the next sweep includes it naturally.
	var landings []storage.Node
	if err := db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND status != ?",
		true, false, "landing", "both", "provisioning").Find(&landings).Error; err != nil {
		return nil, err
	}
	req := &Request{
		Now: now, Policy: policy, Entries: entries, Landings: landings,
		Conns:   LatestConns(db),
		Sick:    LatestSick(db),
		Quality: LatestQuality(db),
		Manual:  map[uint]bool{},
		Current: map[uint]*storage.LandingAssignment{},
	}
	for _, entry := range entries {
		manual, err := hasManualAssignment(db, entry.ID, entry.Region)
		if err != nil {
			return nil, err
		}
		if manual {
			req.Manual[entry.ID] = true
		}
		cur, err := currentAuto(db, entry.ID, foldDirection(entry.Direction))
		if err != nil {
			return nil, err
		}
		if cur != nil {
			req.Current[entry.ID] = cur
		}
	}
	return req, nil
}

// Decide evaluates every schedulable entry against the request (F-1023):
// collect and rank candidates, apply the switch margins against the current
// landing, and produce one Decision per entry. It is pure -- no DB access, no
// writes -- so the same input always yields the same decisions. Entries with
// an effective manual assignment produce no decision at all (manual wins,
// matching the sweep's historical behavior).
func Decide(req *Request) []Decision {
	offPeak := InOffPeak(req.Now, req.Policy)
	var out []Decision
	for _, entry := range req.Entries {
		if req.Manual[entry.ID] {
			continue
		}
		direction := foldDirection(entry.Direction)
		policyName := policyFor(req.Policy, direction)
		d := Decision{
			EntryID: entry.ID, EntryName: entry.Name,
			Direction: direction, Policy: policyName,
		}
		cands := collectCandidates(req, entry, direction, &d)

		// The entry itself must be able to run the relay data plane toward the
		// landing it is picking; otherwise the tunnel cannot come up.
		if !storage.HasCapability(entry, storage.CapRelayEntry) {
			d.Action = ActionSkip
			d.Reason = "entry lacks relay_entry capability"
			out = append(out, d)
			continue
		}

		weights := req.Policy.Weights()
		d.Candidates = rankCandidates(cands, policyName, direction, req.Policy.MissingCost, weights)

		idx, ok := Pick(policyName, direction, req.Policy.MissingCost, weights, cands)
		if !ok {
			d.Action = ActionNone
			d.Reason = "no allocatable candidate"
			out = append(out, d)
			continue
		}
		winner := cands[idx].Node
		d.WinnerID = winner.ID

		// When the current landing is among healthy candidates, use the conservative peak check; when not (sick/offline/none),
		// force the switch -- health issues do not queue.
		curIdx := -1
		if current := req.Current[entry.ID]; current != nil {
			for i := range cands {
				if cands[i].Healthy && cands[i].Node.ID == current.LandingNodeID {
					curIdx = i
					break
				}
			}
		}
		if curIdx >= 0 {
			if curIdx == idx {
				d.Action = ActionKeep
				d.Reason = "current is already the best"
				out = append(out, d)
				continue
			}
			// At peak the primary metric must be clearly better to move; the off-peak window rebalances by the policy total order.
			if !offPeak && !shouldSwitch(policyName, direction, req.Policy.MissingCost, weights, cands, curIdx, idx) {
				d.Action = ActionKeep
				d.Reason = "peak switch margin not met"
				out = append(out, d)
				continue
			}
		}
		c := cands[idx]
		d.Action = ActionSwitch
		d.ToID = winner.ID
		if current := req.Current[entry.ID]; current != nil {
			d.FromID = current.LandingNodeID
		}
		d.Reason = fmt.Sprintf("%s 负载 %d 连/%dM", policyName, c.Conns, c.CapacityMbps())
		out = append(out, d)
	}
	return out
}

// collectCandidates builds the scoring inputs for one entry: direction-
// matched, capability-gated landings with their load/health/cost signals.
// Cost-ceiling exclusions (F-0606) are appended to d.Exclusions instead of
// being trailed immediately, so Decide stays write-free and the executor
// records them once per decision.
func collectCandidates(req *Request, entry storage.Node, direction string, d *Decision) []Candidate {
	cands := make([]Candidate, 0, len(req.Landings))
	for _, l := range req.Landings {
		if l.ID == entry.ID {
			continue
		}
		if l.Direction != direction && l.Direction != agentproto.DirectionBoth {
			continue
		}
		// Capability gate (F-0230): the landing must declare relay_landing
		// and must not report it off; nodes without any stored list fall back
		// to the role-derived set, so legacy registrations stay eligible.
		if !storage.HasCapability(l, storage.CapRelayLanding) {
			continue
		}
		// Price freshness (F-0604): node updated_at is the price as-of
		// marker; beyond the window the price is stale and must not drive
		// the cost decision (it degrades per MissingCost instead).
		known := l.BillingType != "按流量" || l.TrafficPriceCents > 0
		fresh := req.Now.Sub(l.UpdatedAt) < billing.PriceFreshWindow
		// Cost ceiling (F-0606): metered candidates with a known fresh price
		// above the ceiling are excluded this round and the exclusion is
		// trailed (1-hour dedupe). Unknown or stale prices are never
		// excluded (F-0610: an unknown price must not be guessed as
		// over-budget), and fixed billing has no marginal cost at all.
		if req.Policy.MaxCostPerGB > 0 && l.BillingType == "按流量" && known && fresh &&
			l.TrafficPriceCents > req.Policy.MaxCostPerGB {
			d.Exclusions = append(d.Exclusions, Exclusion{
				LandingID: l.ID,
				Reason:    fmt.Sprintf("单价 %d 分/GB 超上限 %d", l.TrafficPriceCents, req.Policy.MaxCostPerGB),
			})
			continue
		}
		cands = append(cands, Candidate{Node: l, Conns: req.Conns[l.ID], Healthy: !req.Sick[l.ID],
			CostKnown: known, CostFresh: fresh, Quality: req.Quality[l.ID]})
	}
	return cands
}

// rankCandidates orders the evaluated candidates exactly like the sweep's
// comparator (policy total order with the missing-price partition, then
// name), so the decision output reads in the order the policy ranks them and
// matches the panel preview.
func rankCandidates(cands []Candidate, policy, direction, missingRule string, w ScoreWeights) []CandidateEval {
	evals := make([]CandidateEval, len(cands))
	for i, c := range cands {
		evals[i] = CandidateEval{Candidate: c, Eligible: c.Healthy, QualityScore: QualityScore(c.Quality)}
		if !c.Healthy {
			evals[i].Reason = "probe window reports sick"
		}
	}
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	less := LessFor(policy, direction, missingRule, w, cands)
	sort.SliceStable(idx, func(a, b int) bool { return less(idx[a], idx[b]) })
	ordered := make([]CandidateEval, len(evals))
	for pos, i := range idx {
		ordered[pos] = evals[i]
	}
	return ordered
}

// Apply executes decisions (F-1023): trails every cost-ceiling exclusion
// (with the dedupe window, so repeating sweeps do not flood the trail), then
// releases and creates assignment rows for switches. Returns the switch
// events the hooks and the log expect.
func Apply(db *gorm.DB, req *Request, decisions []Decision) ([]SwitchEvent, error) {
	var events []SwitchEvent
	for _, d := range decisions {
		for _, ex := range d.Exclusions {
			// Best-effort trail (the sweep ignored recording errors too): the
			// filter already applied, so a failure only costs observability.
			_ = recordExclusion(db, d.EntryID, ex.LandingID, d.Direction, d.Policy, ex.Reason, req.Now)
		}
		if d.Action != ActionSwitch {
			continue
		}
		current := req.Current[d.EntryID]
		if current != nil {
			if err := db.Model(&storage.LandingAssignment{}).Where("id = ?", current.ID).
				Updates(map[string]any{"released_at": req.Now, "release_reason": "自动换线"}).Error; err != nil {
				return events, err
			}
		}
		row := storage.LandingAssignment{
			EntryNodeID:   &d.EntryID,
			LandingNodeID: d.ToID,
			Direction:     d.Direction,
			Strategy:      d.Policy,
			Weight:        0,
			Reason:        d.Reason,
			AssignedAt:    req.Now,
		}
		if err := db.Create(&row).Error; err != nil {
			return events, err
		}
		fromName := "（无）"
		if current != nil {
			fromName = nodeName(req.Landings, current.LandingNodeID)
		}
		events = append(events, SwitchEvent{
			EntryID:   d.EntryID,
			EntryName: d.EntryName,
			FromName:  fromName,
			ToName:    nodeName(req.Landings, d.ToID),
			Direction: d.Direction,
			Policy:    d.Policy,
			Reason:    d.Reason,
		})
	}
	return events, nil
}
