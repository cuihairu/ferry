package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Evening peak return-traffic report (E-29): inbound return-traffic quality
// (aggregated direction=in probe verdicts) bucketed by hour, with the evening
// peak (19-23h) summarized separately and compared against off-peak. Average
// latency counts only reachable samples; availability is the reachable ratio.

const (
	eveningDefaultDays = 7
	eveningMaxDays     = 30
	eveningPeakStart   = 19 // [19,23) half-open, same convention as the low-peak rebalance window
	eveningPeakEnd     = 23
)

// EveningHour is the inbound return-traffic quality of one clock hour.
type EveningHour struct {
	Hour            int     `json:"hour"`
	Samples         int     `json:"samples"`
	AvgRttMs        float64 `json:"avg_rtt_ms"`       // avg latency over reachable samples
	AvgLossPct      float64 `json:"avg_loss_pct"`     // avg packet loss over all samples
	AvailabilityPct float64 `json:"availability_pct"` // -1=no samples
}

// EveningWindow is the summary comparison block for evening peak / off-peak.
type EveningWindow struct {
	Samples         int     `json:"samples"`
	AvgRttMs        float64 `json:"avg_rtt_ms"`
	AvgLossPct      float64 `json:"avg_loss_pct"`
	AvailabilityPct float64 `json:"availability_pct"` // -1=no samples
}

// EveningReport is the evening peak return-traffic report: 24 hour rows plus
// the evening-peak / off-peak summaries.
type EveningReport struct {
	Days    int           `json:"days"`
	Hours   []EveningHour `json:"hours"`
	Peak    EveningWindow `json:"peak"`    // 19-23h
	OffPeak EveningWindow `json:"offpeak"` // the remaining hours
}

// getEvening returns the evening peak return-traffic report (GET /api/evening?days=7).
func (h *Handler) getEvening(c *gin.Context) {
	days := eveningDefaultDays
	if v := c.Query("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > eveningMaxDays {
			fail(c, http.StatusBadRequest, errors.New("days must be 1-30"))
			return
		}
		days = n
	}
	since := time.Now().AddDate(0, 0, -days)
	var rows []storage.ProbeReport
	if err := h.db.Where("direction = ? AND probed_at >= ?", "in", since).
		Order("probed_at ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, buildEvening(days, rows))
}

// eveningAcc aggregation accumulator: sample count, reachable-sample latency sum, loss sum.
type eveningAcc struct {
	n       int
	rttN    int
	rttSum  int64
	lossSum int64
}

func (a *eveningAcc) add(r storage.ProbeReport) {
	a.n++
	a.lossSum += int64(r.LossPct)
	if r.Reachable {
		a.rttN++
		a.rttSum += int64(r.RttMs)
	}
}

// buildEvening buckets probe verdicts by local-time-zone hour and summarizes the evening peak and off-peak separately.
func buildEvening(days int, rows []storage.ProbeReport) EveningReport {
	hours := [24]eveningAcc{}
	peak, offpeak := eveningAcc{}, eveningAcc{}
	for _, r := range rows {
		hour := r.ProbedAt.Local().Hour()
		hours[hour].add(r)
		if hour >= eveningPeakStart && hour < eveningPeakEnd {
			peak.add(r)
		} else {
			offpeak.add(r)
		}
	}
	rep := EveningReport{Days: days, Hours: make([]EveningHour, 24)}
	for h := 0; h < 24; h++ {
		a := hours[h]
		row := EveningHour{Hour: h, Samples: a.n, AvailabilityPct: -1}
		if a.n > 0 {
			row.AvgLossPct = float64(a.lossSum) / float64(a.n)
			row.AvailabilityPct = float64(a.rttN) / float64(a.n) * 100
			row.AvgRttMs = eveningAvgRtt(a)
		}
		rep.Hours[h] = row
	}
	rep.Peak = eveningWindow(peak)
	rep.OffPeak = eveningWindow(offpeak)
	return rep
}

func eveningAvgRtt(a eveningAcc) float64 {
	if a.rttN == 0 {
		return 0
	}
	return float64(a.rttSum) / float64(a.rttN)
}

func eveningWindow(a eveningAcc) EveningWindow {
	w := EveningWindow{Samples: a.n, AvailabilityPct: -1}
	if a.n > 0 {
		w.AvgLossPct = float64(a.lossSum) / float64(a.n)
		w.AvailabilityPct = float64(a.rttN) / float64(a.n) * 100
		w.AvgRttMs = eveningAvgRtt(a)
	}
	return w
}
