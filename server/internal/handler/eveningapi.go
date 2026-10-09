package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 晚高峰回程报表（E-29）：按小时分段的回程质量（direction=in 探测结论聚合），
// 晚高峰（19–23 时）单独汇总与平峰对比。平均延迟只计可达样本，可用率即可达比例。

const (
	eveningDefaultDays = 7
	eveningMaxDays     = 30
	eveningPeakStart   = 19 // [19,23) 左闭右开，与低峰再平衡窗口同口径
	eveningPeakEnd     = 23
)

// EveningHour 是一个钟点段的回程质量。
type EveningHour struct {
	Hour            int     `json:"hour"`
	Samples         int     `json:"samples"`
	AvgRttMs        float64 `json:"avg_rtt_ms"`       // 可达样本的平均延迟
	AvgLossPct      float64 `json:"avg_loss_pct"`     // 全部样本的平均丢包
	AvailabilityPct float64 `json:"availability_pct"` // -1=无样本
}

// EveningWindow 是晚高峰/平峰的汇总对比块。
type EveningWindow struct {
	Samples         int     `json:"samples"`
	AvgRttMs        float64 `json:"avg_rtt_ms"`
	AvgLossPct      float64 `json:"avg_loss_pct"`
	AvailabilityPct float64 `json:"availability_pct"` // -1=无样本
}

// EveningReport 是晚高峰回程报表：24 小时明细 + 晚高峰/平峰汇总。
type EveningReport struct {
	Days    int           `json:"days"`
	Hours   []EveningHour `json:"hours"`
	Peak    EveningWindow `json:"peak"`    // 19–23 时
	OffPeak EveningWindow `json:"offpeak"` // 其余时段
}

// getEvening 返回晚高峰回程报表（GET /api/evening?days=7）。
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

// eveningAcc 聚合累加器：样本数、可达样本延迟和、丢包和。
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

// buildEvening 把探测结论按本地时区钟点分桶，晚高峰与平峰各汇总一份。
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
