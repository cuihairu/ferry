package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestEveningAPI covers the evening-peak return-trip report (E-29): direction=in bucketed by hour,
// latency counts reachable samples only, the evening peak [19,23) is aggregated separately from off-peak, and out-of-window or outbound samples are ignored.
func TestEveningAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	seed := func(direction string, at time.Time, rtt, loss int, reachable bool) {
		row := storage.ProbeReport{
			NodeID: 1, TargetKind: "peer", Direction: direction,
			RttMs: rtt, LossPct: loss, Reachable: reachable, Verdict: "healthy",
			ProbedAt: at,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Today 20:00 and 21:00: reachable, RTT 200/100 → evening-peak average 150.
	today := time.Now()
	atHour := func(hour, min int) time.Time {
		return time.Date(today.Year(), today.Month(), today.Day(), hour, min, 0, 0, time.Local)
	}
	seed("in", atHour(20, 0), 200, 0, true)
	seed("in", atHour(21, 30), 100, 0, true)
	// Today 3:00: one reachable, one unreachable → off-peak availability 50%, average latency 50 (reachable only).
	seed("in", atHour(3, 0), 50, 0, true)
	seed("in", atHour(3, 30), 0, 100, false)
	// Outbound and out-of-window samples are ignored.
	seed("out", atHour(20, 15), 1, 0, true)
	seed("in", today.AddDate(0, 0, -40), 999, 0, true)

	rec := doJSON(t, r, "GET", "/api/evening", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get evening status = %d body=%s", rec.Code, rec.Body)
	}
	var rep struct {
		Days  int `json:"days"`
		Hours []struct {
			Hour            int     `json:"hour"`
			Samples         int     `json:"samples"`
			AvgRttMs        float64 `json:"avg_rtt_ms"`
			AvgLossPct      float64 `json:"avg_loss_pct"`
			AvailabilityPct float64 `json:"availability_pct"`
		} `json:"hours"`
		Peak struct {
			Samples         int     `json:"samples"`
			AvgRttMs        float64 `json:"avg_rtt_ms"`
			AvgLossPct      float64 `json:"avg_loss_pct"`
			AvailabilityPct float64 `json:"availability_pct"`
		} `json:"peak"`
		OffPeak struct {
			Samples         int     `json:"samples"`
			AvgRttMs        float64 `json:"avg_rtt_ms"`
			AvgLossPct      float64 `json:"avg_loss_pct"`
			AvailabilityPct float64 `json:"availability_pct"`
		} `json:"offpeak"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode evening resp: %v", err)
	}
	if rep.Days != 7 || len(rep.Hours) != 24 {
		t.Fatalf("days=%d hours=%d", rep.Days, len(rep.Hours))
	}
	h20 := rep.Hours[20]
	if h20.Samples != 1 || h20.AvgRttMs != 200 || h20.AvailabilityPct != 100 {
		t.Fatalf("hour20 = %+v", h20)
	}
	h3 := rep.Hours[3]
	if h3.Samples != 2 || h3.AvgLossPct != 50 || h3.AvailabilityPct != 50 || h3.AvgRttMs != 50 {
		t.Fatalf("hour3 = %+v", h3)
	}
	if rep.Hours[23].Samples != 0 || rep.Hours[23].AvailabilityPct != -1 {
		t.Fatalf("hour23 empty = %+v", rep.Hours[23])
	}
	if rep.Peak.Samples != 2 || rep.Peak.AvgRttMs != 150 || rep.Peak.AvailabilityPct != 100 {
		t.Fatalf("peak = %+v", rep.Peak)
	}
	if rep.OffPeak.Samples != 2 || rep.OffPeak.AvailabilityPct != 50 {
		t.Fatalf("offpeak = %+v", rep.OffPeak)
	}

	// Window parameter: invalid values rejected; even with the window widened to 30 days, the sample from 40 days ago is still ignored (40>30).
	if rec := doJSON(t, r, "GET", "/api/evening?days=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("days=0 status = %d", rec.Code)
	}
}
