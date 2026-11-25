package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestEveningValidDays covers an explicit days value taking effect (a legal value other than the default 7 lands in the bucket).
func TestEveningValidDays(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	rec := doJSON(t, r, http.MethodGet, "/api/evening?days=3", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("days=3: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Days int `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Days != 3 {
		t.Fatalf("days = %d, want 3", out.Days)
	}
}

// TestEveningDBError covers the probe-verdict fetch failure 500.
func TestEveningDBError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:evening_rows", db, (*[]storage.ProbeReport)(nil))
	if rec := doJSON(t, r, http.MethodGet, "/api/evening", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list probes: %d %s", rec.Code, rec.Body)
	}
}

// TestEveningAllUnreachable covers an hour bucket that has samples but no reachable ones: average latency
// takes the zero-value branch (not counted in the average), availability is 0.
func TestEveningAllUnreachable(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	atHour := func(hour int) time.Time {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.Local)
	}
	if err := db.Create(&storage.ProbeReport{
		NodeID: 1, TargetKind: "peer", Direction: "in",
		RttMs: 0, LossPct: 100, Reachable: false, Verdict: "down",
		ProbedAt: atHour(eveningPeakStart),
	}).Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, http.MethodGet, "/api/evening", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("evening: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Hours []struct {
			Hour            int     `json:"hour"`
			Samples         int     `json:"samples"`
			AvgRttMs        float64 `json:"avg_rtt_ms"`
			AvailabilityPct float64 `json:"availability_pct"`
		} `json:"hours"`
		Peak struct {
			Samples         int     `json:"samples"`
			AvgRttMs        float64 `json:"avg_rtt_ms"`
			AvailabilityPct float64 `json:"availability_pct"`
		} `json:"peak"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var hit struct {
		Samples         int
		AvgRttMs        float64
		AvailabilityPct float64
	}
	for _, h := range out.Hours {
		if h.Hour == eveningPeakStart {
			hit.Samples, hit.AvgRttMs, hit.AvailabilityPct = h.Samples, h.AvgRttMs, h.AvailabilityPct
		}
	}
	if hit.Samples != 1 || hit.AvgRttMs != 0 || hit.AvailabilityPct != 0 {
		t.Fatalf("unreachable hour = %+v", hit)
	}
	if out.Peak.Samples != 1 || out.Peak.AvgRttMs != 0 || out.Peak.AvailabilityPct != 0 {
		t.Fatalf("peak window = %+v", out.Peak)
	}
}
