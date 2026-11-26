package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/outlier"
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
	AvgRttMs        float64 `json:"avg_rtt_ms"`       // avg latency over reachable, outlier-trimmed samples
	AvgLossPct      float64 `json:"avg_loss_pct"`     // avg packet loss over all samples
	AvailabilityPct float64 `json:"availability_pct"` // -1=no samples
	// Outliers is how many reachable RTT samples the F-0916 median/MAD filter
	// removed from this hour's average.
	Outliers int `json:"outliers"`
}

// EveningWindow is the summary comparison block for evening peak / off-peak.
type EveningWindow struct {
	Samples         int     `json:"samples"`
	AvgRttMs        float64 `json:"avg_rtt_ms"`
	AvgLossPct      float64 `json:"avg_loss_pct"`
	AvailabilityPct float64 `json:"availability_pct"` // -1=no samples
	// Outliers follows EveningHour.Outliers over the whole window.
	Outliers int `json:"outliers"`
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

// eveningAcc aggregation accumulator: sample count, reachable-sample latency
// samples (kept individually for the F-0916 outlier trim), loss sum.
type eveningAcc struct {
	n       int
	rtts    []int
	lossSum int64
}

func (a *eveningAcc) add(r storage.ProbeReport) {
	a.n++
	a.lossSum += int64(r.LossPct)
	if r.Reachable {
		a.rtts = append(a.rtts, r.RttMs)
	}
}

// eveningRTT is the outlier-trimmed average of the reachable samples (F-0916)
// plus the count of samples the trim removed.
func eveningRTT(a eveningAcc) (float64, int) {
	trimmed := outlier.Trim(a.rtts, outlier.DefaultFactor)
	if len(trimmed) == 0 {
		return 0, 0
	}
	sum := 0
	for _, s := range trimmed {
		sum += s
	}
	return float64(sum) / float64(len(trimmed)), len(a.rtts) - len(trimmed)
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
			row.AvailabilityPct = float64(len(a.rtts)) / float64(a.n) * 100
			row.AvgRttMs, row.Outliers = eveningRTT(a)
		}
		rep.Hours[h] = row
	}
	rep.Peak = eveningWindow(peak)
	rep.OffPeak = eveningWindow(offpeak)
	return rep
}

func eveningWindow(a eveningAcc) EveningWindow {
	w := EveningWindow{Samples: a.n, AvailabilityPct: -1}
	if a.n > 0 {
		w.AvgLossPct = float64(a.lossSum) / float64(a.n)
		w.AvailabilityPct = float64(len(a.rtts)) / float64(a.n) * 100
		w.AvgRttMs, w.Outliers = eveningRTT(a)
	}
	return w
}
