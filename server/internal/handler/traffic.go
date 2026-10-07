package handler

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxTrafficBatch 限制单批记账条数，防误传大载荷。
const maxTrafficBatch = 1000

// usageQuery 返回用户已用流量的窗口化查询（P1-4）：reset_cycle 为
// day/week/month 时只算当前窗口内的记录，none=全量累计。
func (h *Handler) usageQuery(userID uint, cycle string, now time.Time) *gorm.DB {
	q := h.db.Model(&storage.TrafficLog{}).Where("user_id = ?", userID)
	if since := quota.WindowStart(cycle, now); !since.IsZero() {
		q = q.Where("recorded_at >= ?", since)
	}
	return q
}

// recordTraffic 写入用户级流量记账（P0-9），单条或批量共用 items 数组。
func (h *Handler) recordTraffic(c *gin.Context) {
	var in model.TrafficLogBatchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.Items) == 0 || len(in.Items) > maxTrafficBatch {
		fail(c, http.StatusBadRequest, errors.New("items must be 1-1000"))
		return
	}

	now := time.Now()
	rows := make([]storage.TrafficLog, 0, len(in.Items))
	userIDs := map[uint]bool{}
	nodeIDs := map[uint]bool{}
	for i, item := range in.Items {
		if item.UserID == 0 {
			fail(c, http.StatusBadRequest, fmt.Errorf("items[%d].user_id is required", i))
			return
		}
		if item.RxBytes < 0 || item.TxBytes < 0 {
			fail(c, http.StatusBadRequest, fmt.Errorf("items[%d] bytes must be >= 0", i))
			return
		}
		recordedAt := now
		if item.RecordedAt != nil {
			recordedAt = *item.RecordedAt
		}
		rows = append(rows, storage.TrafficLog{
			UserID:     item.UserID,
			NodeID:     item.NodeID,
			RxBytes:    item.RxBytes,
			TxBytes:    item.TxBytes,
			RecordedAt: recordedAt,
		})
		userIDs[item.UserID] = true
		if item.NodeID != nil {
			nodeIDs[*item.NodeID] = true
		}
	}
	if err := h.checkIDsExist(&storage.User{}, userIDs, "user_id"); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.checkIDsExist(&storage.Node{}, nodeIDs, "node_id"); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.db.Create(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"recorded": len(rows)})
}

// checkIDsExist 校验引用的 id 集合都存在，报出首个缺失值。
func (h *Handler) checkIDsExist(model any, ids map[uint]bool, field string) error {
	if len(ids) == 0 {
		return nil
	}
	var found []uint
	if err := h.db.Model(model).Where("id IN ?", keysOf(ids)).Pluck("id", &found).Error; err != nil {
		return err
	}
	if len(found) == len(ids) {
		return nil
	}
	have := map[uint]bool{}
	for _, id := range found {
		have[id] = true
	}
	for _, id := range keysOf(ids) {
		if !have[id] {
			return fmt.Errorf("unknown %s %d", field, id)
		}
	}
	return nil
}

func keysOf(ids map[uint]bool) []uint {
	out := make([]uint, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	return out
}

// userTrafficDay 是按日聚合的一行。
type userTrafficDay struct {
	Date string `json:"date"`
	Rx   int64  `json:"rx"`
	Tx   int64  `json:"tx"`
}

// userTrafficSummary 是 GET /api/users/:id/traffic 的响应（P0-10）。
type userTrafficSummary struct {
	UserID     uint             `json:"user_id"`
	TotalRx    int64            `json:"total_rx"`
	TotalTx    int64            `json:"total_tx"`
	QuotaBytes int64            `json:"quota_bytes"`
	Days       []userTrafficDay `json:"days"`
}

// userTraffic 按用户汇总用量并给出按日明细；日分组在 Go 侧做，避开 DATE() 三方言扫描差异。
func (h *Handler) userTraffic(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var rows []storage.TrafficLog
	if err := h.db.Select("rx_bytes", "tx_bytes", "recorded_at").
		Where("user_id = ?", u.ID).Order("recorded_at").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	summary := userTrafficSummary{UserID: u.ID, QuotaBytes: u.QuotaBytes, Days: []userTrafficDay{}}
	byDay := map[string]*userTrafficDay{}
	var order []string
	for _, r := range rows {
		summary.TotalRx += r.RxBytes
		summary.TotalTx += r.TxBytes
		day := r.RecordedAt.Format("2006-01-02")
		d := byDay[day]
		if d == nil {
			d = &userTrafficDay{Date: day}
			byDay[day] = d
			order = append(order, day)
		}
		d.Rx += r.RxBytes
		d.Tx += r.TxBytes
	}
	for _, day := range order {
		summary.Days = append(summary.Days, *byDay[day])
	}
	c.JSON(http.StatusOK, summary)
}
