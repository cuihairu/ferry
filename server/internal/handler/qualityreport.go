package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/quality"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Quality reports (F-0949): the per-business scores rolled up by region+ISP
// and by hour of day. Operators pick a business class; the report answers
// "which region/ISP is worst for gaming right now" and "which hours decay" --
// one scored view, two rollup dimensions, no per-dimension special cases.

// qualityRegionRow is one region+ISP rollup: the mean score plus the raw
// transport means so a bad score can be explained without re-querying.
type qualityRegionRow struct {
	Region      string `json:"region"`
	ISP         string `json:"isp"`
	Samples     int    `json:"samples"`
	AvgScore    int    `json:"avg_score"`
	AvgRttMs    int    `json:"avg_rtt_ms"`
	AvgJitterMs int    `json:"avg_jitter_ms"`
	AvgLossPct  int    `json:"avg_loss_pct"`
}

// qualityTimeslotRow is one hour-of-day rollup: how this business usually
// scores at that hour across the window (evening decay shows up directly).
type qualityTimeslotRow struct {
	Hour     int `json:"hour"` // 0-23, server-local
	Samples  int `json:"samples"`
	AvgScore int `json:"avg_score"`
}

type qualityReport struct {
	Business  string               `json:"business"`
	WindowHs  int                  `json:"window_hours"`
	Regions   []qualityRegionRow   `json:"regions"`
	Timeslots []qualityTimeslotRow `json:"timeslots"`
}

// qualityReport serves GET /api/quality-report.
func (h *Handler) qualityReport(c *gin.Context) {
	business := c.Query("business")
	switch business {
	case quality.Game, quality.Web, quality.Video, quality.Proxy:
	default:
		fail(c, http.StatusBadRequest, errors.New("business must be game/web/video/proxy"))
		return
	}
	windowHs := 24
	if v := c.Query("window_hours"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 168 {
			fail(c, http.StatusBadRequest, errors.New("window_hours must be 1-168"))
			return
		}
		windowHs = n
	}
	since := time.Now().Add(-time.Duration(windowHs) * time.Hour)
	var rows []storage.ProbeReport
	if err := h.db.Where("probed_at > ?", since).
		Order("probed_at ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	type tally struct {
		n                        int
		score, rtt, jitter, loss int64
	}
	type regionKey struct{ region, isp string }
	byRegion := map[regionKey]*tally{}
	byHour := map[int]*tally{}
	for _, r := range rows {
		score, scored := quality.Score(business, r)
		if !scored {
			continue // unscored reports never become invented averages
		}
		rk := regionKey{r.Region, r.ISP}
		t := byRegion[rk]
		if t == nil {
			t = &tally{}
			byRegion[rk] = t
		}
		t.n++
		t.score += int64(score)
		t.rtt += int64(r.RttMs)
		t.jitter += int64(r.JitterMs)
		t.loss += int64(r.LossPct)

		hh := byHour[r.ProbedAt.Hour()]
		if hh == nil {
			hh = &tally{}
			byHour[r.ProbedAt.Hour()] = hh
		}
		hh.n++
		hh.score += int64(score)
	}

	out := qualityReport{Business: business, WindowHs: windowHs,
		Regions: []qualityRegionRow{}, Timeslots: []qualityTimeslotRow{}}
	for rk, t := range byRegion {
		out.Regions = append(out.Regions, qualityRegionRow{
			Region:      rk.region,
			ISP:         rk.isp,
			Samples:     t.n,
			AvgScore:    int(t.score / int64(t.n)),
			AvgRttMs:    int(t.rtt / int64(t.n)),
			AvgJitterMs: int(t.jitter / int64(t.n)),
			AvgLossPct:  int(t.loss / int64(t.n)),
		})
	}
	for hour, t := range byHour {
		out.Timeslots = append(out.Timeslots, qualityTimeslotRow{
			Hour: hour, Samples: t.n, AvgScore: int(t.score / int64(t.n)),
		})
	}
	c.JSON(http.StatusOK, out)
}
