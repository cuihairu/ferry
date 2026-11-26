package handler

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestNodeStatusAggregate covers GET /api/nodes/:id/status (F-0750): agent,
// relay and probe states in one call; absent relay/probe history stays null.
func TestNodeStatusAggregate(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	node := storage.Node{Name: "st-a", Token: "tk-st-a", Role: "relay",
		Status: "online", Enabled: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("node: %v", err)
	}
	now := time.Now()
	seen := now.Add(-2 * time.Minute)
	if err := db.Model(&node).Updates(map[string]any{
		"last_seen": &seen, "maintenance": true,
	}).Error; err != nil {
		t.Fatalf("pin last_seen: %v", err)
	}
	// Empty history: relay and probe blocks are null, agent is always present.
	rec := doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(node.ID)+"/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"online"`) || !strings.Contains(body, `"maintenance":true`) {
		t.Fatalf("agent state: %s", body)
	}
	if !strings.Contains(body, `"relay":null`) || !strings.Contains(body, `"probe":null`) {
		t.Fatalf("missing history must be null: %s", body)
	}

	// Relog a relay process snapshot and one probe report.
	if err := db.Create(&storage.NodeProcStatus{
		NodeID: node.ID, Proc: "relay", State: "running", PID: 42, Restarts: 1,
		Version: "v1.2.3", UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("proc status: %v", err)
	}
	if err := db.Create(&storage.ProbeReport{
		NodeID: node.ID, TargetKind: "tunnel", Direction: "out",
		RttMs: 120, LossPct: 2, Reachable: true, Verdict: "healthy", ProbedAt: now,
	}).Error; err != nil {
		t.Fatalf("probe report: %v", err)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(node.ID)+"/status", nil)
	body = rec.Body.String()
	for _, want := range []string{`"state":"running"`, `"pid":42`, `"role":"relay"`,
		`"version":"v1.2.3"`, `"verdict":"healthy"`, `"rtt_ms":120`, `"loss_pct":2`} {
		if !strings.Contains(body, want) {
			t.Fatalf("aggregate must contain %s: %s", want, body)
		}
	}

	// Unknown node stays a 404.
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/9999/status", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown node must be 404: %d", rec.Code)
	}
}

// TestProbeReportsNodeFilter covers the per-node filter on GET
// /api/probe-reports: only the requested prober's reports come back.
func TestProbeReportsNodeFilter(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	a := storage.Node{Name: "pf-a", Token: "tk-pf-a"}
	b := storage.Node{Name: "pf-b", Token: "tk-pf-b"}
	for _, n := range []*storage.Node{&a, &b} {
		if err := db.Create(n).Error; err != nil {
			t.Fatalf("node: %v", err)
		}
	}
	at := time.Now()
	for _, rep := range []storage.ProbeReport{
		{NodeID: a.ID, TargetKind: "tunnel", Verdict: "healthy", ProbedAt: at},
		{NodeID: b.ID, TargetKind: "tunnel", Verdict: "sick", ProbedAt: at},
	} {
		if err := db.Create(&rep).Error; err != nil {
			t.Fatalf("probe report: %v", err)
		}
	}
	rec := doJSON(t, r, http.MethodGet, "/api/probe-reports?node_id="+itoa(a.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"verdict":"healthy"`) || strings.Contains(body, `"verdict":"sick"`) {
		t.Fatalf("filter must return only node a: %s", body)
	}
	if rec := doJSON(t, r, http.MethodGet, "/api/probe-reports?node_id=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad node_id must be 400: %d", rec.Code)
	}
}

// TestNodeStatusHealth covers the F-0902 wiring: the status aggregate carries
// the node's own derived health, distinct from the per-path health endpoint.
func TestNodeStatusHealth(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	live := storage.Node{Name: "nh-live", Token: "tk-nh-live", Status: "online", Enabled: true}
	susp := storage.Node{Name: "nh-susp", Token: "tk-nh-susp", Status: "online",
		PoolState: "suspended", Enabled: true}
	nodes := []storage.Node{live, susp}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatalf("node: %v", err)
		}
	}
	// Live node without verdicts: unknown (absence is not health); suspended
	// node: degraded even before any verdict.
	rec := doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(nodes[0].ID)+"/status", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"health":"unknown"`) {
		t.Fatalf("live health: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, r, http.MethodGet, "/api/nodes/"+itoa(nodes[1].ID)+"/status", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"health":"degraded"`) {
		t.Fatalf("suspended health: %d %s", rec.Code, rec.Body.String())
	}
}
