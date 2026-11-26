package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestQualityReport covers GET /api/quality-report (F-0949): reports roll up
// per region+ISP with mean score and transport means, per hour-of-day for
// the timeslot view, unscored rows stay out, and bad params are rejected.
func TestQualityReport(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	_, _ = createProbeNode(t, r)

	seed := func(region, isp string, hourOffset int, rtt, jitter, loss int, reachable bool) {
		t.Helper()
		if err := db.Create(&storage.ProbeReport{
			NodeID: 1, TargetKind: "exit", TargetHost: "baseline.example",
			RttMs: rtt, JitterMs: jitter, LossPct: loss, Reachable: reachable,
			Verdict: "healthy", Region: region, ISP: isp,
			ProbedAt: time.Now().Add(-time.Duration(hourOffset) * time.Hour),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 华东/电信: two strong game reports (score 100 each: rtt<=40, jitter<=5, no loss).
	seed("华东", "电信", 0, 30, 2, 0, true)
	seed("华东", "电信", 1, 40, 5, 0, true)
	// 华东/联通: one mid report (rtt 220 -> component 50; jitter 10 -> 93.3;
	// loss 1 -> 80; avail 99 -> game score = 0.4*50+0.2*93.3+0.2*80+0.2*99 = 74).
	seed("华东", "联通", 0, 220, 10, 1, true)
	// one unreachable row: no phase datums, loss 100 -> score 0, still tallied.
	seed("华东", "联通", 5, 0, 0, 100, false)

	rec := doJSON(t, r, "GET", "/api/quality-report?business=game&window_hours=24", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("report: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Business string `json:"business"`
		WindowHs int    `json:"window_hours"`
		Regions  []struct {
			Region      string `json:"region"`
			ISP         string `json:"isp"`
			Samples     int    `json:"samples"`
			AvgScore    int    `json:"avg_score"`
			AvgRttMs    int    `json:"avg_rtt_ms"`
			AvgJitterMs int    `json:"avg_jitter_ms"`
			AvgLossPct  int    `json:"avg_loss_pct"`
		} `json:"regions"`
		Timeslots []struct {
			Hour     int `json:"hour"`
			Samples  int `json:"samples"`
			AvgScore int `json:"avg_score"`
		} `json:"timeslots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Business != "game" || out.WindowHs != 24 {
		t.Fatalf("header = %s/%d", out.Business, out.WindowHs)
	}

	byISP := map[string]struct {
		samples, avgScore int
	}{}
	for _, row := range out.Regions {
		if row.Region != "华东" {
			t.Fatalf("region = %q", row.Region)
		}
		byISP[row.ISP] = struct {
			samples, avgScore int
		}{row.Samples, row.AvgScore}
	}
	tc := byISP["电信"]
	if tc.samples != 2 || tc.avgScore != 100 {
		t.Fatalf("电信 rollup = %+v; want 2 samples at 100", tc)
	}
	cu := byISP["联通"]
	if cu.samples != 2 {
		t.Fatalf("联通 samples = %d; want 2", cu.samples)
	}
	// 联通: scores 74 and 0 -> mean 37.
	if cu.avgScore != 37 {
		t.Fatalf("联通 avg = %d; want 37", cu.avgScore)
	}

	// timeslots: hour of now and one hour ago share the same bucket when now
	// lands there; both reports sit in the current hour band.
	if len(out.Timeslots) == 0 {
		t.Fatal("timeslots must not be empty")
	}
	samples := 0
	for _, ts := range out.Timeslots {
		if ts.Hour < 0 || ts.Hour > 23 {
			t.Fatalf("hour = %d", ts.Hour)
		}
		samples += ts.Samples
	}
	if samples != 4 {
		t.Fatalf("timeslot samples = %d; want 4", samples)
	}

	// bad business and bad window are rejected before touching the db.
	if rec := doJSON(t, r, "GET", "/api/quality-report?business=vending", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad business: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/quality-report?business=game&window_hours=500", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/quality-report", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing business: %d", rec.Code)
	}

	// A proxy report with no scoring rule for the class... proxy has a set but
	// no rule; every row unscored -> empty arrays, not an error.
	rec = doJSON(t, r, "GET", "/api/quality-report?business=proxy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy report: %d %s", rec.Code, rec.Body)
	}
	var proxyOut map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &proxyOut)
	if regions, _ := proxyOut["regions"].([]any); len(regions) != 0 {
		t.Fatalf("proxy must be unscored: %v", proxyOut["regions"])
	}
}
