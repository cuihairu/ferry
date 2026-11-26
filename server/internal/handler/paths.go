package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Path management (F-0105/F-0243): named entry -> (optional relay) -> landing
// access paths with a transport label. Validation pins the node links to real
// rows; the multi-link and enable-disable semantics live in the model shape
// (no unique constraint on entry/landing, an Enabled flag).

type pathInput struct {
	Name            string  `json:"name"`
	EntryNodeID     *uint   `json:"entry_node_id"`
	LandingNodeID   *uint   `json:"landing_node_id"`
	RelayNodeID     *uint   `json:"relay_node_id"`
	Transport       string  `json:"transport"`
	Region          string  `json:"region"`
	Direction       string  `json:"direction"`
	Enabled         *bool   `json:"enabled"`
	Note            *string `json:"note"`
	ProbeImportance string  `json:"probe_importance,omitempty"` // F-0924: critical/normal/low
}

// validatePathInput checks the referenced nodes exist and normalizes the
// labels; the returned row carries the merged values.
func (h *Handler) validatePathInput(in pathInput) (storage.Path, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return storage.Path{}, errors.New("name is required (1-64 chars)")
	}
	if in.EntryNodeID == nil || *in.EntryNodeID == 0 {
		return storage.Path{}, errors.New("entry_node_id is required")
	}
	if in.LandingNodeID == nil || *in.LandingNodeID == 0 {
		return storage.Path{}, errors.New("landing_node_id is required")
	}
	// Fresh dest per lookup: a reused struct keeps its primary key, and GORM
	// folds that non-zero pk into the next query as a hidden condition.
	var entryNode storage.Node
	if err := h.db.Where("id = ?", *in.EntryNodeID).First(&entryNode).Error; err != nil {
		return storage.Path{}, errors.New("entry node not found")
	}
	var landingNode storage.Node
	if err := h.db.Where("id = ?", *in.LandingNodeID).First(&landingNode).Error; err != nil {
		return storage.Path{}, errors.New("landing node not found")
	}
	if in.RelayNodeID != nil && *in.RelayNodeID != 0 {
		var relayNode storage.Node
		if err := h.db.Where("id = ?", *in.RelayNodeID).First(&relayNode).Error; err != nil {
			return storage.Path{}, errors.New("relay node not found")
		}
	}
	direction := in.Direction
	if direction == "" {
		direction = "out"
	}
	if direction != "out" && direction != "in" {
		return storage.Path{}, errors.New("direction must be out/in")
	}
	transport := in.Transport
	if transport == "" {
		transport = "tls"
	}
	relay := in.RelayNodeID
	if relay != nil && *relay == 0 {
		relay = nil
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// Probe importance grades the path's dispatched tunnel-probe frequency
	// (F-0924): critical probes every minute, low every quarter hour.
	importance := "normal"
	if in.ProbeImportance != "" {
		importance = in.ProbeImportance
	}
	if importance != "critical" && importance != "normal" && importance != "low" {
		return storage.Path{}, errors.New("probe_importance must be critical/normal/low")
	}
	return storage.Path{
		Name:            name,
		EntryNodeID:     *in.EntryNodeID,
		LandingNodeID:   *in.LandingNodeID,
		RelayNodeID:     relay,
		Transport:       transport,
		Region:          in.Region,
		Direction:       direction,
		Enabled:         enabled,
		ProbeImportance: importance,
		Version:         1,
	}, nil
}

func (h *Handler) listPaths(c *gin.Context) {
	q := h.db.Model(&storage.Path{})
	if v := c.Query("entry_node_id"); v != "" {
		q = q.Where("entry_node_id = ?", v)
	}
	if v := c.Query("landing_node_id"); v != "" {
		q = q.Where("landing_node_id = ?", v)
	}
	if v := c.Query("region"); v != "" {
		q = q.Where("region = ?", v)
	}
	if v := c.Query("direction"); v != "" {
		q = q.Where("direction = ?", v)
	}
	if v := c.Query("enabled"); v != "" {
		q = q.Where("enabled = ?", v == "true")
	}
	var paths []storage.Path
	if err := q.Order("id DESC").Find(&paths).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paths": paths})
}

func (h *Handler) getPath(c *gin.Context) {
	var p storage.Path
	if err := h.db.First(&p, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("path not found"))
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) createPath(c *gin.Context) {
	var in pathInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p, err := h.validatePathInput(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Note != nil {
		p.Note = *in.Note
	}
	if err := h.db.Create(&p).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// Enabled carries default:true, so an explicit false needs a direct write.
	if in.Enabled != nil && !*in.Enabled {
		if err := h.db.Model(&storage.Path{}).Where("id = ?", p.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		p.Enabled = false
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) updatePath(c *gin.Context) {
	var cur storage.Path
	if err := h.db.First(&cur, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("path not found"))
		return
	}
	var in pathInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// Absent fields keep their current values (merged validation).
	merged := in
	if merged.Name == "" {
		merged.Name = cur.Name
	}
	if merged.EntryNodeID == nil {
		v := cur.EntryNodeID
		merged.EntryNodeID = &v
	}
	if merged.LandingNodeID == nil {
		v := cur.LandingNodeID
		merged.LandingNodeID = &v
	}
	if merged.RelayNodeID == nil {
		merged.RelayNodeID = cur.RelayNodeID
	}
	if merged.Transport == "" {
		merged.Transport = cur.Transport
	}
	if merged.Region == "" {
		merged.Region = cur.Region
	}
	if merged.Direction == "" {
		merged.Direction = cur.Direction
	}
	if merged.Enabled == nil {
		v := cur.Enabled
		merged.Enabled = &v
	}
	if merged.ProbeImportance == "" {
		merged.ProbeImportance = cur.ProbeImportance
	}
	next, err := h.validatePathInput(merged)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Note != nil {
		next.Note = *in.Note
	} else {
		next.Note = cur.Note
	}
	next.ID = cur.ID
	next.CreatedAt = cur.CreatedAt
	next.Version = cur.Version + 1
	if err := h.db.Model(&storage.Path{}).Where("id = ?", cur.ID).Select("name", "entry_node_id",
		"landing_node_id", "relay_node_id", "transport", "region", "direction", "enabled",
		"probe_importance", "version", "note", "updated_at").
		Updates(&next).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var out storage.Path
	if err := h.db.First(&out, cur.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) deletePath(c *gin.Context) {
	// Dependency check (F-0254): a path referenced by a group is not deletable
	// until it is removed from the group.
	id := c.Param("id")
	var members int64
	if err := h.db.Model(&storage.PathGroupMember{}).Where("path_id = ?", id).Count(&members).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if members > 0 {
		fail(c, http.StatusConflict, errors.New("path is a member of a path group; remove it first"))
		return
	}
	res := h.db.Delete(&storage.Path{}, id)
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("path not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
