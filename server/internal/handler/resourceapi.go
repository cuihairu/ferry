package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/cloudsync"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Cloud resource API (F-0346/F-0353~F-0356): list/detail, manual sync,
// sync history and Node binding. Resources are soft-deleted by the sync
// engine (F-0351), so detail stays queryable after the vendor drops a
// resource, and the binding never destroys Node history.

type cloudResourceInput struct {
	// NodeID: 0/null unbinds; a positive id binds (F-0353/F-0354).
	NodeID *uint `json:"node_id"`
}

func (h *Handler) listCloudResources(c *gin.Context) {
	q := storage.CloudResource{}
	if s := c.Query("provider_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid provider_id"))
			return
		}
		q.ProviderID = uint(id)
	}
	if s := c.Query("region"); s != "" {
		q.Region = s
	}
	if s := c.Query("state"); s != "" {
		if !cloudprov.ValidState(s) {
			fail(c, http.StatusBadRequest, errors.New("invalid state"))
			return
		}
		q.State = s
	}
	if s := c.Query("node_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid node_id"))
			return
		}
		n := uint(id)
		q.NodeID = &n
	}
	if s := c.Query("tag_key"); s != "" {
		// F-0345: post-filter by a tag key/value after the DB query, since
		// Tags is a JSON text column in SQLite.
		val := c.Query("tag_value")
		var rows []storage.CloudResource
		if err := h.db.Where(&q).Order("id ASC").Find(&rows).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		filtered := make([]storage.CloudResource, 0, len(rows))
		for _, row := range rows {
			var tags map[string]string
			if jsonErr := json.Unmarshal([]byte(row.Tags), &tags); jsonErr != nil {
				continue
			}
			if v, ok := tags[s]; ok && (val == "" || v == val) {
				filtered = append(filtered, row)
			}
		}
		c.JSON(http.StatusOK, filtered)
		return
	}
	var rows []storage.CloudResource
	if err := h.db.Where(&q).Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *Handler) getCloudResource(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	var row storage.CloudResource
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	c.JSON(http.StatusOK, row)
}

// bindCloudResource points a resource at a Node. The unique index on NodeID
// prevents a Node being claimed twice; a resource being rebound moves its
// binding (F-0353/F-0355).
func (h *Handler) bindCloudResource(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	var row storage.CloudResource
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	var in cloudResourceInput
	if err := c.ShouldBindJSON(&in); err != nil || in.NodeID == nil || *in.NodeID == 0 {
		fail(c, http.StatusBadRequest, errors.New("node_id is required"))
		return
	}
	var n int64
	if err := h.db.Model(&storage.Node{}).Where("id = ?", *in.NodeID).Count(&n).Error; err != nil || n == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	// The resource may be bound to one Node only: inserting a second row
	// pointing at the same Node would violate the unique index. Check up front
	// for a clean conflict message (F-0355).
	var other storage.CloudResource
	err = h.db.Where("node_id = ? AND id <> ?", *in.NodeID, row.ID).First(&other).Error
	if err == nil {
		fail(c, http.StatusConflict, errors.New("node already bound to another cloud resource"))
		return
	}
	if err := h.db.Model(&row).Updates(map[string]any{"node_id": *in.NodeID, "unbound_at": nil}).Error; err != nil {
		fail(c, http.StatusConflict, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": row.ID, "node_id": *in.NodeID})
}

// unbindCloudResource keeps the resource row but clears its Node link
// (F-0354/F-0351); UnboundAt records when the link was dropped.
func (h *Handler) unbindCloudResource(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not found"})
		return
	}
	res := h.db.Model(&storage.CloudResource{}).Where("id = ? AND node_id IS NOT NULL", id).
		Updates(map[string]any{"node_id": nil, "unbound_at": time.Now()})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "resource not bound"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// syncCloudResources triggers one manual run (F-0346). The scope and tag
// filter come from the request; the account scope is enforced by the sync
// engine. The response carries run counts and the resource list.
func (h *Handler) syncCloudResources(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	var in struct {
		Region string            `json:"region"`
		Tags   map[string]string `json:"tags"`
	}
	_ = c.ShouldBindJSON(&in)
	cfg := cloudsync.SyncConfig{Region: strings.TrimSpace(in.Region), Tags: in.Tags}
	result, _, err := cloudsync.SyncAndLog(c.Request.Context(), h.db, h.secrets, uint(id), "manual", cfg, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !result.OK {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": result.Error})
		return
	}
	var rows []storage.CloudResource
	if err := h.db.Where("provider_id = ?", id).Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "added": result.Added,
		"updated": result.Updated, "missing": result.Missing, "resources": rows})
}

// listCloudSyncLogs exposes the sync history (F-0349/F-0356).
func (h *Handler) listCloudSyncLogs(c *gin.Context) {
	var rows []storage.CloudSyncLog
	q := h.db.Order("id DESC")
	if s := c.Query("provider_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid provider_id"))
			return
		}
		q = q.Where("provider_id = ?", id)
	}
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}
