package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAllocAPI covers policy view/set (E-21): default policy, persisted set,
// rejection of invalid tiers, and the auto row list (no manual rows mixed in).
func TestAllocAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Default policy.
	rec := doJSON(t, r, "GET", "/api/alloc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get alloc status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Policy struct {
			Out            string `json:"out"`
			In             string `json:"in"`
			RebalanceStart int    `json:"rebalance_start"`
			RebalanceEnd   int    `json:"rebalance_end"`
		} `json:"policy"`
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode alloc resp: %v", err)
	}
	if out.Policy.Out != "cost_first" || out.Policy.In != "perf_first" || out.Policy.RebalanceStart != 1 || out.Policy.RebalanceEnd != 7 {
		t.Fatalf("default policy = %+v", out.Policy)
	}

	// Set tiers and the off-peak window, then read them back.
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "cost_first", "in": "perf_first", "rebalance_start": 2, "rebalance_end": 6,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put policy status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Policy.Out != "cost_first" || out.Policy.In != "perf_first" || out.Policy.RebalanceStart != 2 || out.Policy.RebalanceEnd != 6 {
		t.Fatalf("policy after put = %+v", out.Policy)
	}

	// An invalid window is rejected.
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "balanced", "in": "balanced", "rebalance_start": 9, "rebalance_end": 9,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid window status = %d", rec.Code)
	}

	// An invalid tier is rejected.
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "greedy", "in": "balanced",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy status = %d", rec.Code)
	}

	// An empty tier means fall back to the direction default (E-30): out is
	// cost_first, in is perf_first, and the window is kept.
	rec = doJSON(t, r, "PUT", "/api/alloc/policy", map[string]any{
		"out": "", "in": "", "rebalance_start": 3, "rebalance_end": 8,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("empty policy status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Policy.Out != "cost_first" || out.Policy.In != "perf_first" || out.Policy.RebalanceStart != 3 || out.Policy.RebalanceEnd != 8 {
		t.Fatalf("policy reset to defaults = %+v", out.Policy)
	}

	// auto rows appear in the list; manual rows do not.
	if err := db.Exec(`INSERT INTO landing_assignments
		(entry_node_id, landing_node_id, direction, strategy, assigned_at)
		VALUES (1, 2, 'out', 'least_conn', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO landing_assignments
		(entry_node_id, landing_node_id, direction, strategy, assigned_at)
		VALUES (1, 3, 'out', 'manual', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0]["strategy"] != "least_conn" {
		t.Fatalf("rows = %+v", out.Rows)
	}
}

// TestAllocDecisionsList covers the snapshot read API (F-1032): newest first
// with a bounded limit, and bad limits rejected.
func TestAllocDecisionsList(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// an empty table still answers with an empty list
	rec := doJSON(t, r, "GET", "/api/alloc/decisions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list status = %d body=%s", rec.Code, rec.Body)
	}

	if err := db.Create(&storage.AllocDecision{Kind: "alloc", RoundAt: time.Now(),
		PolicyOut: "cost_first", PolicyIn: "perf_first", Entries: 2, Landings: 3,
		Switches: 1, Decisions: `[{"entry_name":"e1","action":"switch"}]`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.AllocDecision{Kind: "failover", RoundAt: time.Now(),
		Switches: 2, Decisions: `[]`}).Error; err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, r, "GET", "/api/alloc/decisions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Rows []struct {
			Kind      string `json:"kind"`
			Switches  int    `json:"switches"`
			Decisions string `json:"decisions"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode decisions: %v", err)
	}
	if len(out.Rows) != 2 || out.Rows[0].Kind != "failover" || out.Rows[1].Kind != "alloc" {
		t.Fatalf("rows = %+v", out.Rows)
	}

	// the limit is honored
	rec = doJSON(t, r, "GET", "/api/alloc/decisions?limit=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("limit status = %d", rec.Code)
	}
	out.Rows = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("limit=1 rows = %d", len(out.Rows))
	}

	// out-of-range and non-numeric limits are rejected
	for _, bad := range []string{"0", "201", "abc"} {
		rec = doJSON(t, r, "GET", "/api/alloc/decisions?limit="+bad, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit %q status = %d", bad, rec.Code)
		}
	}
}

// TestAllocExplain covers the explain API (F-1034): a read-only round over
// live signals with per-candidate eligibility, structural exclusions with
// names, the entry_id filter, and manual entries listed apart.
func TestAllocExplain(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	entry := storage.Node{Name: "ex-entry", Token: "tok-ex-entry", Role: "entry",
		Direction: "out", Enabled: true,
		Capabilities: storage.MustJSON([]string{"proxy_ingress", "relay_entry", "probe"})}
	cheap := storage.Node{Name: "ex-cheap", Token: "tok-ex-cheap", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 5,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	pricey := storage.Node{Name: "ex-pricey", Token: "tok-ex-pricey", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 50,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	inOnly := storage.Node{Name: "ex-inonly", Token: "tok-ex-inonly", Role: "landing",
		Direction: "in", Enabled: true,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	for _, n := range []*storage.Node{&entry, &cheap, &pricey, &inOnly} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}

	type candView struct {
		LandingID uint   `json:"landing_id"`
		Name      string `json:"name"`
		Eligible  bool   `json:"eligible"`
		Reason    string `json:"reason"`
	}
	type exclView struct {
		LandingID uint   `json:"landing_id"`
		Name      string `json:"name"`
		Reason    string `json:"reason"`
	}
	type decView struct {
		EntryID    uint       `json:"entry_id"`
		Action     string     `json:"action"`
		Current    string     `json:"current"`
		To         string     `json:"to"`
		Reason     string     `json:"reason"`
		Candidates []candView `json:"candidates"`
		Exclusions []exclView `json:"exclusions"`
	}
	var out struct {
		Decisions []decView `json:"decisions"`
		Manual    []uint    `json:"manual"`
	}

	// full view: the cheap landing wins, the in-only landing is excluded by name
	rec := doJSON(t, r, "GET", "/api/alloc/explain", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain status = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode explain: %v", err)
	}
	if len(out.Decisions) != 1 || len(out.Manual) != 0 {
		t.Fatalf("explain = %+v", out)
	}
	d := out.Decisions[0]
	if d.EntryID != entry.ID || d.Action != "switch" || d.To != cheap.Name || d.Reason == "" {
		t.Fatalf("decision = %+v", d)
	}
	if len(d.Candidates) != 2 {
		t.Fatalf("candidates = %+v", d.Candidates)
	}
	if d.Candidates[0].Name != cheap.Name || !d.Candidates[0].Eligible {
		t.Fatalf("rank[0] = %+v", d.Candidates[0])
	}
	if d.Candidates[1].Name != pricey.Name || !d.Candidates[1].Eligible {
		t.Fatalf("rank[1] = %+v", d.Candidates[1])
	}
	if len(d.Exclusions) != 1 || d.Exclusions[0].LandingID != inOnly.ID ||
		d.Exclusions[0].Name != inOnly.Name || d.Exclusions[0].Reason == "" {
		t.Fatalf("exclusions = %+v", d.Exclusions)
	}

	// a manual assignment moves the entry out of decisions into the manual list
	eid := entry.ID
	if err := db.Create(&storage.LandingAssignment{
		EntryNodeID: &eid, LandingNodeID: pricey.ID, Direction: "out",
		Strategy: "manual", AssignedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/alloc/explain", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain(manual) status = %d", rec.Code)
	}
	out.Decisions, out.Manual = nil, nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Decisions) != 0 || len(out.Manual) != 1 || out.Manual[0] != entry.ID {
		t.Fatalf("manual view = %+v", out)
	}

	// the entry filter narrows the view; a bad id is rejected
	rec = doJSON(t, r, "GET", "/api/alloc/explain?entry_id="+strconv.Itoa(int(entry.ID)), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered status = %d", rec.Code)
	}
	out.Decisions, out.Manual = nil, nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Decisions) != 0 || len(out.Manual) != 1 {
		t.Fatalf("filtered manual view = %+v", out)
	}
	rec = doJSON(t, r, "GET", "/api/alloc/explain?entry_id=abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad entry_id status = %d", rec.Code)
	}
}

// TestAllocSimulate covers the simulator API (F-1035): optional overrides are
// merged onto the live policy, validated like SavePolicy, and the round is
// decided without writing anything -- the persisted policy and the assignment
// table stay untouched.
func TestAllocSimulate(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	entry := storage.Node{Name: "sim-entry", Token: "tok-sim-entry", Role: "entry",
		Direction: "out", Enabled: true,
		Capabilities: storage.MustJSON([]string{"proxy_ingress", "relay_entry", "probe"})}
	cheap := storage.Node{Name: "sim-cheap", Token: "tok-sim-cheap", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 5,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	pricey := storage.Node{Name: "sim-pricey", Token: "tok-sim-pricey", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 50,
		Capabilities: storage.MustJSON([]string{"relay_landing"})}
	for _, n := range []*storage.Node{&entry, &cheap, &pricey} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}

	var sim struct {
		Simulated bool   `json:"simulated"`
		RoundAt   string `json:"round_at"`
		Policy    struct {
			Out          string `json:"out"`
			MaxCostPerGB int64  `json:"max_cost_per_gb"`
		} `json:"policy"`
		Decisions []struct {
			Action     string `json:"action"`
			To         string `json:"to"`
			Exclusions []struct {
				Name   string `json:"name"`
				Reason string `json:"reason"`
			} `json:"exclusions"`
		} `json:"decisions"`
	}

	// an empty body re-decides the live policy: the cheap landing wins
	rec := doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("simulate status = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sim); err != nil {
		t.Fatalf("decode simulate: %v", err)
	}
	if !sim.Simulated || sim.Policy.Out != "cost_first" || len(sim.Decisions) != 1 ||
		sim.Decisions[0].Action != "switch" || sim.Decisions[0].To != cheap.Name {
		t.Fatalf("baseline simulate = %+v", sim)
	}

	// a cost ceiling of 3 excludes the cheap landing by name -> nothing allocatable
	rec = doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{"max_cost_per_gb": 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("ceiling simulate status = %d", rec.Code)
	}
	sim = struct {
		Simulated bool   `json:"simulated"`
		RoundAt   string `json:"round_at"`
		Policy    struct {
			Out          string `json:"out"`
			MaxCostPerGB int64  `json:"max_cost_per_gb"`
		} `json:"policy"`
		Decisions []struct {
			Action     string `json:"action"`
			To         string `json:"to"`
			Exclusions []struct {
				Name   string `json:"name"`
				Reason string `json:"reason"`
			} `json:"exclusions"`
		} `json:"decisions"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &sim); err != nil {
		t.Fatal(err)
	}
	if sim.Policy.MaxCostPerGB != 3 || len(sim.Decisions) != 1 || sim.Decisions[0].Action != "none" {
		t.Fatalf("ceiling simulate = %+v", sim)
	}
	// both metered landings sit above the ceiling, so both are excluded by name
	if len(sim.Decisions[0].Exclusions) != 2 ||
		sim.Decisions[0].Exclusions[0].Name != cheap.Name ||
		sim.Decisions[0].Exclusions[1].Name != pricey.Name ||
		sim.Decisions[0].Exclusions[0].Reason == "" {
		t.Fatalf("ceiling exclusions = %+v", sim.Decisions[0].Exclusions)
	}

	// an invalid tier is rejected like SavePolicy would
	rec = doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{"out": "greedy"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid tier status = %d", rec.Code)
	}

	// the hour override moves the simulated round within today
	rec = doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{"hour": 3})
	if rec.Code != http.StatusOK {
		t.Fatalf("hour simulate status = %d", rec.Code)
	}
	sim.Decisions = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &sim); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sim.RoundAt, "T03:00:00") {
		t.Fatalf("simulated round_at = %s, want hour 3", sim.RoundAt)
	}
	rec = doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{"hour": 24})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("hour 24 status = %d", rec.Code)
	}

	// nothing persisted: the live policy still has no ceiling
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get alloc status = %d", rec.Code)
	}
	var live struct {
		Policy struct {
			Out          string `json:"out"`
			MaxCostPerGB int64  `json:"max_cost_per_gb"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if live.Policy.Out != "cost_first" || live.Policy.MaxCostPerGB != 0 {
		t.Fatalf("simulate persisted policy: %+v", live.Policy)
	}
}

// TestAllocPolicyPriority covers the priority policy mode end to end
// (F-1042): the tier is accepted by the policy API, the explain view serves
// the operator's top tier over the cheap one, and the simulator can flip the
// same fleet back to cost_first without persisting either side.
func TestAllocPolicyPriority(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// out=priority persists and reads back
	rec := doJSON(t, r, "PUT", "/api/alloc/policy",
		map[string]any{"out": "priority", "in": "perf_first", "rebalance_start": 1, "rebalance_end": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("put priority status = %d body=%s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	var pol struct {
		Policy struct {
			Out string `json:"out"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pol); err != nil {
		t.Fatal(err)
	}
	if pol.Policy.Out != "priority" {
		t.Fatalf("policy out = %s", pol.Policy.Out)
	}

	// fleet: tier-0 pricey landing beats the cheap tier-3 one
	entry := storage.Node{Name: "pp-entry", Token: "tok-pp-entry", Role: "entry",
		Direction: "out", Enabled: true,
		Capabilities: storage.MustJSON([]string{"proxy_ingress", "relay_entry", "probe"})}
	cheap := storage.Node{Name: "pp-cheap", Token: "tok-pp-cheap", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 5,
		SchedPriority: 3, Capabilities: storage.MustJSON([]string{"relay_landing"})}
	top := storage.Node{Name: "pp-top", Token: "tok-pp-top", Role: "landing",
		Direction: "out", Enabled: true, BillingType: "按流量", TrafficPriceCents: 90,
		SchedPriority: 0, Capabilities: storage.MustJSON([]string{"relay_landing"})}
	for _, n := range []*storage.Node{&entry, &cheap, &top} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}

	var expl struct {
		Decisions []struct {
			Action string `json:"action"`
			To     string `json:"to"`
		} `json:"decisions"`
	}
	rec = doJSON(t, r, "GET", "/api/alloc/explain", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain status = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &expl); err != nil {
		t.Fatal(err)
	}
	if len(expl.Decisions) != 1 || expl.Decisions[0].Action != "switch" || expl.Decisions[0].To != top.Name {
		t.Fatalf("priority explain = %+v", expl.Decisions)
	}

	// simulating cost_first on this fleet still serves the tier-0 landing:
	// the F-1029 tier partition governs every policy, and the priority policy
	// only changes the within-tier order (name, not load/cost) and the peak rule
	var sim struct {
		Policy struct {
			Out string `json:"out"`
		} `json:"policy"`
		Decisions []struct {
			Action string `json:"action"`
			To     string `json:"to"`
		} `json:"decisions"`
	}
	rec = doJSON(t, r, "POST", "/api/alloc/simulate", map[string]any{"out": "cost_first"})
	if rec.Code != http.StatusOK {
		t.Fatalf("simulate status = %d body=%s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sim); err != nil {
		t.Fatal(err)
	}
	if sim.Policy.Out != "cost_first" || len(sim.Decisions) != 1 ||
		sim.Decisions[0].Action != "switch" || sim.Decisions[0].To != top.Name {
		t.Fatalf("simulated cost_first = %+v", sim)
	}

	// and the live policy still says priority
	rec = doJSON(t, r, "GET", "/api/alloc", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &pol); err != nil {
		t.Fatal(err)
	}
	if pol.Policy.Out != "priority" {
		t.Fatalf("simulate persisted policy: %+v", pol.Policy)
	}
}
