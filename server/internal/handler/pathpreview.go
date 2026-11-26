package handler

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/billing"
	"github.com/cuihairu/ferry/server/internal/nodehealth"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Candidate path preview (F-1015): for a given entry node, enumerate the
// landing candidates the allocation sweep would consider, ranked by the
// configured policy, each with its health state and eligibility. The
// preview changes nothing -- it answers "if I created a path from this entry,
// which landing would it land on and why" without writing a row.

// previewCandidate is one ranked landing: the node facts an operator needs to
// judge a candidate, plus why it is (or is not) eligible.
type previewCandidate struct {
	NodeID       uint    `json:"node_id"`
	Name         string  `json:"name"`
	Region       string  `json:"region"`
	ISP          string  `json:"isp"`
	ASN          string  `json:"asn"`
	LineType     string  `json:"line_type"`
	Direction    string  `json:"direction"`
	CapacityMbps int     `json:"capacity_mbps"`
	Conns        int     `json:"conns"`
	CostPerGB    int64   `json:"cost_per_gb"`
	Healthy      bool    `json:"healthy"`
	Health       string  `json:"health"`
	Score        float64 `json:"score"`
	Eligible     bool    `json:"eligible"`
	Reason       string  `json:"reason"`
}

// pathPreview is the whole answer: the ranked candidate list plus which one
// the policy would actually pick.
type pathPreview struct {
	EntryNodeID uint               `json:"entry_node_id"`
	Direction   string             `json:"direction"`
	Policy      string             `json:"policy"`
	WinnerID    uint               `json:"winner_id"`
	Candidates  []previewCandidate `json:"candidates"`
}

// previewLimit caps the ranked list returned to the panel; the sweep is not
// capped, but the preview is a UI affordance.
const previewLimit = 50

// previewPathCandidates serves GET /api/paths/preview?entry_node_id=&direction=.
func (h *Handler) previewPathCandidates(c *gin.Context) {
	entryID := queryUint(c, "entry_node_id")
	if entryID == 0 {
		fail(c, http.StatusBadRequest, errors.New("entry_node_id is required"))
		return
	}
	var entry storage.Node
	if err := h.db.First(&entry, entryID).Error; err != nil {
		fail(c, http.StatusNotFound, errNotFound)
		return
	}
	direction := strings.TrimSpace(c.Query("direction"))
	if direction == "" {
		direction = entry.Direction
		if direction == agentproto.DirectionBoth {
			direction = agentproto.DirectionOut
		}
	}
	if direction != agentproto.DirectionIn && direction != agentproto.DirectionOut {
		fail(c, http.StatusBadRequest, errors.New("direction must be in/out"))
		return
	}

	var landings []storage.Node
	if err := h.db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND status != ?",
		true, false, "landing", "both", "provisioning").Find(&landings).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	policy, err := alloc.LoadPolicy(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	tier := policy.Out
	if direction == agentproto.DirectionIn {
		tier = policy.In
	}
	if !alloc.ValidPolicy(tier) {
		tier = alloc.DefaultPolicyOut
	}

	now := time.Now()
	conns := alloc.LatestConns(h.db)
	sick := alloc.LatestSick(h.db)

	cands := make([]alloc.Candidate, 0, len(landings))
	for _, l := range landings {
		if l.ID == entry.ID {
			continue
		}
		if l.Direction != direction && l.Direction != agentproto.DirectionBoth {
			continue
		}
		// Capability gate (F-0230): the landing must declare relay_landing
		// and must not report it off; legacy nodes fall back to the
		// role-derived set, so they stay eligible.
		if !storage.HasCapability(l, storage.CapRelayLanding) {
			continue
		}
		known := l.BillingType != "按流量" || l.TrafficPriceCents > 0
		fresh := now.Sub(l.UpdatedAt) < billing.PriceFreshWindow
		cands = append(cands, alloc.Candidate{
			Node: l, Conns: conns[l.ID], Healthy: !sick[l.ID],
			CostKnown: known, CostFresh: fresh,
		})
	}

	// The entry must be able to run the relay data plane toward the landing it
	// picks; otherwise the tunnel cannot come up.
	entryCapable := storage.HasCapability(entry, storage.CapRelayEntry)

	// Node health is diagnostic here: the sweep's own gate is the fresh sick
	// verdict (no verdict = healthy for a cold start) plus the entry capability,
	// so the preview must not reject an unknown node the sweep would accept.
	health := nodehealth.Evaluate(h.db, landings, now)

	out := make([]previewCandidate, 0, len(cands))
	for _, cd := range cands {
		h := health[cd.Node.ID]
		reason := "eligible"
		eligible := cd.Healthy && entryCapable
		if !entryCapable {
			reason = "entry lacks relay_entry capability"
		} else if !cd.Healthy {
			reason = "probe window reports sick"
		}
		out = append(out, previewCandidate{
			NodeID:       cd.Node.ID,
			Name:         cd.Node.Name,
			Region:       cd.Node.Region,
			ISP:          cd.Node.ISP,
			ASN:          cd.Node.ASN,
			LineType:     cd.Node.LineType,
			Direction:    cd.Node.Direction,
			CapacityMbps: cd.CapacityMbps(),
			Conns:        cd.Conns,
			CostPerGB:    cd.CostPerGB(),
			Healthy:      cd.Healthy,
			Health:       h,
			Eligible:     eligible,
			Reason:       reason,
		})
	}

	// Rank by the configured policy's comparator, then by name: Pick does the
	// same ranking internally and returns the winner index; the preview needs
	// the ordered list to render, so it applies the same less-for comparator.
	missing := policy.MissingCost
	if !alloc.ValidMissingCost(missing) {
		missing = alloc.DefaultMissingCost
	}
	weights := policy.Weights()
	ordered := rankByPolicy(tier, direction, missing, weights, cands)
	ranked := make([]previewCandidate, 0, len(out))
	for pos, i := range ordered {
		pc := out[i]
		pc.Score = float64(len(ordered)-pos) / float64(len(ordered))
		ranked = append(ranked, pc)
	}

	winnerID := uint(0)
	if len(ranked) > 0 && ranked[0].Eligible {
		winnerID = ranked[0].NodeID
	}
	if len(ranked) > previewLimit {
		ranked = ranked[:previewLimit]
	}

	c.JSON(http.StatusOK, pathPreview{
		EntryNodeID: entry.ID,
		Direction:   direction,
		Policy:      tier,
		WinnerID:    winnerID,
		Candidates:  ranked,
	})
}

// nodeHealthStates evaluates the current node health of a landing set in one
// pass, so the preview does not recompute per candidate.
func (h *Handler) nodeHealthStates(nodes []storage.Node, now time.Time) map[uint]string {
	return nodehealth.Evaluate(h.db, nodes, now)
}

// rankByPolicy orders the candidates exactly like the sweep's Pick comparator:
// policy total order (with the missing-price partition), then name. The
// winner is index 0; the preview renders this same order.
func rankByPolicy(policy, direction, missing string, w alloc.ScoreWeights, cands []alloc.Candidate) []int {
	idx := make([]int, len(cands))
	for i := range idx {
		idx[i] = i
	}
	less := alloc.LessFor(policy, direction, missing, w, cands)
	sort.SliceStable(idx, func(a, b int) bool { return less(idx[a], idx[b]) })
	return idx
}
