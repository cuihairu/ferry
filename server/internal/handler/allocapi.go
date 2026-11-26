package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Landing auto-assignment (E-21) admin side: policy view/set and the current
// auto assignment rows. Assignment decisions run periodically in alloc.Loop;
// here we only read results and edit the policy.

// listAlloc returns the current policy and the effective auto assignment rows.
func (h *Handler) listAlloc(c *gin.Context) {
	policy, err := alloc.LoadPolicy(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rows := []storage.LandingAssignment{}
	if err := h.db.Where("released_at IS NULL AND strategy != ?", alloc.PolicyManual).
		Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy, "rows": rows})
}

// putAllocPolicy sets the assignment tiers for each direction.
func (h *Handler) putAllocPolicy(c *gin.Context) {
	var in alloc.PolicySetting
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := alloc.SavePolicy(h.db, in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, in)
}

// listAllocDecisions returns the recorded scheduling snapshots (F-1032,
// newest first, 50 rows by default): only rounds that actually moved
// something write one, so this is the replay trail of why the assignment
// table looks the way it does.
func (h *Handler) listAllocDecisions(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	rows := []storage.AllocDecision{}
	if err := h.db.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}

// allocExplainCandidate is one ranked landing in an explain view (F-1034):
// the same flat facts the panel preview shows, plus why it is not eligible.
type allocExplainCandidate struct {
	LandingID    uint    `json:"landing_id"`
	Name         string  `json:"name"`
	Eligible     bool    `json:"eligible"`
	Reason       string  `json:"reason,omitempty"`
	QualityScore float64 `json:"quality_score"`
}

// allocExplainExclusion is one landing ruled out before ranking (F-1033).
type allocExplainExclusion struct {
	LandingID uint   `json:"landing_id"`
	Name      string `json:"name"`
	Reason    string `json:"reason"`
}

// allocExplainDecision is one entry's scheduling answer, operator-readable
// (F-1034): what the policy picked, what it moved from and to, why it kept,
// and every candidate's fate.
type allocExplainDecision struct {
	EntryID    uint                    `json:"entry_id"`
	EntryName  string                  `json:"entry_name"`
	Direction  string                  `json:"direction"`
	Policy     string                  `json:"policy"`
	Action     string                  `json:"action"`
	Current    string                  `json:"current,omitempty"`
	From       string                  `json:"from,omitempty"`
	To         string                  `json:"to,omitempty"`
	WinnerID   uint                    `json:"winner_id,omitempty"`
	Reason     string                  `json:"reason,omitempty"`
	Candidates []allocExplainCandidate `json:"candidates"`
	Exclusions []allocExplainExclusion `json:"exclusions"`
}

// explainAlloc renders the current round's decisions read-only (F-1034): the
// unified pipeline (BuildRequest -> Decide) without Apply, so the panel can
// answer "why is this entry on that landing" straight from live signals. An
// optional entry_id narrows the view; entries under a manual assignment are
// listed separately because manual wins and produces no decision.
func (h *Handler) explainAlloc(c *gin.Context) {
	req, err := alloc.BuildRequest(h.db, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	filter := 0
	if v := c.Query("entry_id"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			fail(c, http.StatusBadRequest, errors.New("entry_id must be a positive id"))
			return
		}
		filter = n
	}
	decs := alloc.Decide(req)
	views := []allocExplainDecision{}
	for _, d := range decs {
		if filter != 0 && int(d.EntryID) != filter {
			continue
		}
		views = append(views, renderExplainDecision(req, d))
	}
	manual := []uint{}
	for id := range req.Manual {
		if filter == 0 || int(id) == filter {
			manual = append(manual, id)
		}
	}
	sort.Slice(manual, func(i, j int) bool { return manual[i] < manual[j] })
	c.JSON(http.StatusOK, gin.H{
		"round_at":  req.Now,
		"policy":    req.Policy,
		"decisions": views,
		"manual":    manual,
	})
}

// renderExplainDecision flattens one Decision into the operator view (F-1034),
// resolving landing names from the request model.
func renderExplainDecision(req *alloc.Request, d alloc.Decision) allocExplainDecision {
	landName := make(map[uint]string, len(req.Landings))
	for _, l := range req.Landings {
		landName[l.ID] = l.Name
	}
	view := allocExplainDecision{
		EntryID: d.EntryID, EntryName: d.EntryName, Direction: d.Direction,
		Policy: d.Policy, Action: d.Action, WinnerID: d.WinnerID, Reason: d.Reason,
		Candidates: make([]allocExplainCandidate, 0, len(d.Candidates)),
		Exclusions: make([]allocExplainExclusion, 0, len(d.Exclusions)),
	}
	if cur := req.Current[d.EntryID]; cur != nil {
		view.Current = landName[cur.LandingNodeID]
	}
	view.From = landName[d.FromID]
	view.To = landName[d.ToID]
	for _, ce := range d.Candidates {
		view.Candidates = append(view.Candidates, allocExplainCandidate{
			LandingID: ce.Candidate.Node.ID, Name: ce.Candidate.Node.Name,
			Eligible: ce.Eligible, Reason: ce.Reason, QualityScore: ce.QualityScore,
		})
	}
	for _, ex := range d.Exclusions {
		view.Exclusions = append(view.Exclusions, allocExplainExclusion{
			LandingID: ex.LandingID, Name: landName[ex.LandingID], Reason: ex.Reason,
		})
	}
	return view
}

// allocSimInput is the simulation request (F-1035): every field is optional
// and only the ones present override the live policy, so a panel toggle can
// answer "what would one knob change" without persisting anything.
type allocSimInput struct {
	Out            *string  `json:"out"`
	In             *string  `json:"in"`
	RebalanceStart *int     `json:"rebalance_start"`
	RebalanceEnd   *int     `json:"rebalance_end"`
	WLoad          *float64 `json:"w_load"`
	WCost          *float64 `json:"w_cost"`
	WPremium       *float64 `json:"w_premium"`
	MaxCostPerGB   *int64   `json:"max_cost_per_gb"`
	MissingCost    *string  `json:"missing_cost"`
	// Hour moves the simulated round's clock within today (0-23), so the
	// panel can compare peak vs off-peak behavior for the same fleet.
	Hour *int `json:"hour"`
}

// simulateAlloc runs the decision pipeline on a hypothetical policy (F-1035):
// load live signals, overlay the overrides, validate like SavePolicy, Decide
// -- but never Apply, never write a snapshot, never persist the policy.
func (h *Handler) simulateAlloc(c *gin.Context) {
	var in allocSimInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Hour != nil && (*in.Hour < 0 || *in.Hour > 23) {
		fail(c, http.StatusBadRequest, errors.New("hour must be 0-23"))
		return
	}
	cur, err := alloc.LoadPolicy(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if in.Out != nil {
		cur.Out = *in.Out
	}
	if in.In != nil {
		cur.In = *in.In
	}
	if in.RebalanceStart != nil {
		cur.RebalanceStart = *in.RebalanceStart
	}
	if in.RebalanceEnd != nil {
		cur.RebalanceEnd = *in.RebalanceEnd
	}
	if in.WLoad != nil {
		cur.WLoad = *in.WLoad
	}
	if in.WCost != nil {
		cur.WCost = *in.WCost
	}
	if in.WPremium != nil {
		cur.WPremium = *in.WPremium
	}
	if in.MaxCostPerGB != nil {
		cur.MaxCostPerGB = *in.MaxCostPerGB
	}
	if in.MissingCost != nil {
		cur.MissingCost = *in.MissingCost
	}
	merged, err := alloc.ValidateSetting(cur)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	req, err := alloc.BuildRequest(h.db, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	req.Policy = merged
	if in.Hour != nil {
		req.Now = time.Date(req.Now.Year(), req.Now.Month(), req.Now.Day(), *in.Hour, 0, 0, 0, req.Now.Location())
	}
	views := []allocExplainDecision{}
	for _, d := range alloc.Decide(req) {
		views = append(views, renderExplainDecision(req, d))
	}
	manual := []uint{}
	for id := range req.Manual {
		manual = append(manual, id)
	}
	sort.Slice(manual, func(i, j int) bool { return manual[i] < manual[j] })
	c.JSON(http.StatusOK, gin.H{
		"simulated": true,
		"round_at":  req.Now,
		"policy":    merged,
		"decisions": views,
		"manual":    manual,
	})
}
