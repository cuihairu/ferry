package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 价格关注（E-32）管理端：关注条件 CRUD 与快照查询。扫描与告警在
// cost.WatchLoop（降价/到位经 Herald price_alert），这里只管条件的增删
// 改查与命中动态回显（最新快照 + 较上一次涨跌）。

type watchView struct {
	ID           uint   `json:"id"`
	Provider     string `json:"provider"`
	Region       string `json:"region"`
	Spec         string `json:"spec"`
	TargetPrice  int64  `json:"target_price"`
	Enabled      bool   `json:"enabled"`
	LatestCents  *int64 `json:"latest_cents,omitempty"`
	PrevCents    *int64 `json:"prev_cents,omitempty"`
	ChangePct    *int64 `json:"change_pct,omitempty"` // 最新较上一次：负=降
	AtTarget     bool   `json:"at_target"`            // 现价已到目标价
	LatestURL    string `json:"latest_url,omitempty"`
	CapturedAt   string `json:"captured_at,omitempty"`
	SnapshotDone bool   `json:"snapshot_done"` // 是否已有快照（没跑过扫描前无动态）
}

// watchViewOf 装配单条关注的动态（最近两快照比对）。
func watchViewOf(db *gorm.DB, w storage.PriceWatch) watchView {
	v := watchView{ID: w.ID, Provider: w.Provider, Region: w.Region, Spec: w.Spec,
		TargetPrice: w.TargetPrice, Enabled: w.Enabled}
	var snaps []storage.PriceSnapshot
	if err := db.Where("watch_id = ?", w.ID).Order("id DESC").Limit(2).Find(&snaps).Error; err != nil || len(snaps) == 0 {
		return v // 没跑过扫描前无动态、无到位
	}
	latest := snaps[0]
	latestCents := latest.MonthlyCents
	v.LatestCents = &latestCents
	v.LatestURL = latest.URL
	v.CapturedAt = latest.CapturedAt.Format("2006-01-02 15:04:05")
	v.SnapshotDone = true
	v.AtTarget = w.TargetPrice > 0 && latestCents <= w.TargetPrice
	if len(snaps) > 1 {
		prevCents := snaps[1].MonthlyCents
		v.PrevCents = &prevCents
		if prevCents > 0 {
			pct := (latestCents - prevCents) * 100 / prevCents
			v.ChangePct = &pct
		}
	}
	return v
}

// listWatches 列关注（GET /api/cost/watches，含命中动态）。
func (h *Handler) listWatches(c *gin.Context) {
	var rows []storage.PriceWatch
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]watchView, 0, len(rows))
	for _, w := range rows {
		out = append(out, watchViewOf(h.db, w))
	}
	c.JSON(http.StatusOK, out)
}

type watchInput struct {
	Provider    string `json:"provider"`
	Region      string `json:"region"`
	Spec        string `json:"spec"`
	TargetPrice *int64 `json:"target_price"`
	Enabled     *bool  `json:"enabled"`
}

// watchKey 归一关注键段（小写+去首尾空白），与 costref.Lookup 比对口径一致。
func watchKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// createWatch 新增关注（POST /api/cost/watches）：provider+spec 必填，
// 键唯一（重复 409），target_price 非负（0=只盯降价）。
func (h *Handler) createWatch(c *gin.Context) {
	var in watchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Provider) == "" || strings.TrimSpace(in.Spec) == "" {
		fail(c, http.StatusBadRequest, errors.New("provider and spec are required"))
		return
	}
	target := int64(0)
	if in.TargetPrice != nil {
		if *in.TargetPrice < 0 {
			fail(c, http.StatusBadRequest, errors.New("target_price must be >= 0"))
			return
		}
		target = *in.TargetPrice
	}
	// 键归一（小写+去空格）：与参考价表 costref 词表比对口径一致，
	// 唯一索引才能拦住同键异写的重复关注。
	w := storage.PriceWatch{Provider: watchKey(in.Provider), Region: watchKey(in.Region),
		Spec: watchKey(in.Spec), TargetPrice: target, Enabled: true}
	if in.Enabled != nil {
		w.Enabled = *in.Enabled
	}
	if err := h.db.Create(&w).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			fail(c, http.StatusConflict, errors.New("watch already exists for this price key"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, watchViewOf(h.db, w))
}

// updateWatch 部分更新（PUT /api/cost/watches/:id）：目标价/开关。
func (h *Handler) updateWatch(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("id must be a number"))
		return
	}
	var in watchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var w storage.PriceWatch
	if err := h.db.First(&w, id).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("watch not found"))
		return
	}
	if in.TargetPrice != nil {
		if *in.TargetPrice < 0 {
			fail(c, http.StatusBadRequest, errors.New("target_price must be >= 0"))
			return
		}
		w.TargetPrice = *in.TargetPrice
	}
	if in.Enabled != nil {
		w.Enabled = *in.Enabled
	}
	if err := h.db.Save(&w).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, watchViewOf(h.db, w))
}

// deleteWatch 删除关注并同删快照（应用层级联，对齐设计 DDL CASCADE 口径）。
func (h *Handler) deleteWatch(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("id must be a number"))
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&storage.PriceWatch{}, id).Error; err != nil {
			return err
		}
		return tx.Where("watch_id = ?", id).Delete(&storage.PriceSnapshot{}).Error
	}); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// listWatchSnapshots 快照分页（GET /api/cost/watches/:id/snapshots?limit=，
// id DESC，上限 100）。
func (h *Handler) listWatchSnapshots(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("id must be a number"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	var snaps []storage.PriceSnapshot
	if err := h.db.Where("watch_id = ?", id).Order("id DESC").Limit(limit).Find(&snaps).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, snaps)
}
