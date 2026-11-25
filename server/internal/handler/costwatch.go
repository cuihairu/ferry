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

// Price watch (E-32) admin side: watch condition CRUD and snapshot queries. Scanning and alerting run in
// cost.WatchLoop (price drops / hitting target go through Herald price_alert); here we only handle add/edit/delete/list of conditions
// and replaying the hit dynamics (latest snapshot + change versus the previous one).

type watchView struct {
	ID           uint   `json:"id"`
	Provider     string `json:"provider"`
	Region       string `json:"region"`
	Spec         string `json:"spec"`
	TargetPrice  int64  `json:"target_price"`
	Enabled      bool   `json:"enabled"`
	LatestCents  *int64 `json:"latest_cents,omitempty"`
	PrevCents    *int64 `json:"prev_cents,omitempty"`
	ChangePct    *int64 `json:"change_pct,omitempty"` // latest vs previous: negative = drop
	AtTarget     bool   `json:"at_target"`            // current price has reached the target
	LatestURL    string `json:"latest_url,omitempty"`
	CapturedAt   string `json:"captured_at,omitempty"`
	SnapshotDone bool   `json:"snapshot_done"` // whether a snapshot exists (no dynamics before a scan has run)
}

// watchViewOf assembles the dynamics for one watch (compares the two most recent snapshots).
func watchViewOf(db *gorm.DB, w storage.PriceWatch) watchView {
	v := watchView{ID: w.ID, Provider: w.Provider, Region: w.Region, Spec: w.Spec,
		TargetPrice: w.TargetPrice, Enabled: w.Enabled}
	var snaps []storage.PriceSnapshot
	if err := db.Where("watch_id = ?", w.ID).Order("id DESC").Limit(2).Find(&snaps).Error; err != nil || len(snaps) == 0 {
		return v // no dynamics and no at-target before a scan has run
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

// listWatches lists watches (GET /api/cost/watches, including hit dynamics).
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

// watchKey normalizes watch key segments (lowercase + trim), matching costref.Lookup's comparison contract.
func watchKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// createWatch adds a watch (POST /api/cost/watches): provider+spec are required,
// the key is unique (duplicate → 409), and target_price must be non-negative (0 = only watch for drops).
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
	// Normalize the key (lowercase + trim): matching the reference price table's costref vocabulary comparison
	// contract, so the unique index can catch duplicate watches for the same key in different spellings.
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

// updateWatch partially updates (PUT /api/cost/watches/:id): target price / enabled switch.
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

// deleteWatch deletes a watch and its snapshots together (application-level cascade, aligned with the design's DDL CASCADE contract).
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

// listWatchSnapshots pages snapshots (GET /api/cost/watches/:id/snapshots?limit=,
// id DESC, capped at 100).
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
