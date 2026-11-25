package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// PathGroup management (F-0105/F-0261~F-0267): a named, ordered candidate set
// of paths organized by direction/region/purpose, with a default path and a
// manual exclude flag per member. Health aggregation and subscription policy
// linking stay out of scope here (F-0268~F-0270).

type pathGroupInput struct {
	Name          string  `json:"name"`
	Direction     string  `json:"direction"`
	Region        string  `json:"region"`
	Purpose       string  `json:"purpose"`
	DefaultPathID *uint   `json:"default_path_id"`
	Enabled       *bool   `json:"enabled"`
	Note          *string `json:"note"`
}

func validatePathGroupInput(in pathGroupInput) (storage.PathGroup, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return storage.PathGroup{}, errors.New("name is required (1-64 chars)")
	}
	direction := in.Direction
	if direction == "" {
		direction = "out"
	}
	if direction != "out" && direction != "in" {
		return storage.PathGroup{}, errors.New("direction must be out/in")
	}
	if len(in.Purpose) > 32 {
		return storage.PathGroup{}, errors.New("purpose is too long (max 32 chars)")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return storage.PathGroup{
		Name:      name,
		Direction: direction,
		Region:    in.Region,
		Purpose:   in.Purpose,
		Enabled:   enabled,
	}, nil
}

func (h *Handler) findPathGroup(id string) (*storage.PathGroup, error) {
	var g storage.PathGroup
	if err := h.db.First(&g, id).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

func (h *Handler) listPathGroups(c *gin.Context) {
	q := h.db.Model(&storage.PathGroup{})
	if v := c.Query("direction"); v != "" {
		q = q.Where("direction = ?", v)
	}
	if v := c.Query("region"); v != "" {
		q = q.Where("region = ?", v)
	}
	if v := c.Query("purpose"); v != "" {
		q = q.Where("purpose = ?", v)
	}
	if v := c.Query("enabled"); v != "" {
		q = q.Where("enabled = ?", v == "true")
	}
	var groups []storage.PathGroup
	if err := q.Order("id DESC").Find(&groups).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path_groups": groups})
}

// getPathGroup returns the group with its ordered member list (Position asc).
func (h *Handler) getPathGroup(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	var members []storage.PathGroupMember
	if err := h.db.Where("group_id = ?", g.ID).Order("position ASC, id ASC").Find(&members).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path_group": g, "members": members})
}

func (h *Handler) createPathGroup(c *gin.Context) {
	var in pathGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	g, err := validatePathGroupInput(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Note != nil {
		g.Note = *in.Note
	}
	if err := h.db.Create(&g).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path group name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !g.Enabled {
		// Enabled carries default:true, so an explicit false needs a direct write.
		if err := h.db.Model(&storage.PathGroup{}).Where("id=?", g.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusCreated, g)
}

func (h *Handler) updatePathGroup(c *gin.Context) {
	cur, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	var in pathGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// Absent fields keep their current values (merged validation).
	merged := in
	if merged.Name == "" {
		merged.Name = cur.Name
	}
	if merged.Direction == "" {
		merged.Direction = cur.Direction
	}
	if merged.Region == "" {
		merged.Region = cur.Region
	}
	if merged.Purpose == "" {
		merged.Purpose = cur.Purpose
	}
	if merged.Enabled == nil {
		v := cur.Enabled
		merged.Enabled = &v
	}
	next, err := validatePathGroupInput(merged)
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
	next.DefaultPathID = cur.DefaultPathID
	if in.DefaultPathID != nil {
		// A default must be an existing member of this group (F-0264).
		var m storage.PathGroupMember
		if err := h.db.Where("group_id = ? AND path_id = ?", cur.ID, *in.DefaultPathID).First(&m).Error; err != nil {
			fail(c, http.StatusBadRequest, errors.New("default_path_id must be a member of the group"))
			return
		}
		next.DefaultPathID = in.DefaultPathID
	}
	if err := h.db.Model(&storage.PathGroup{}).Where("id = ?", cur.ID).Select("name", "direction", "region",
		"purpose", "default_path_id", "enabled", "note", "updated_at").
		Updates(&next).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path group name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) deletePathGroup(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	if err := h.db.Where("group_id = ?", g.ID).Delete(&storage.PathGroupMember{}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Delete(g).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

type pathGroupMemberInput struct {
	PathID   *uint `json:"path_id"`
	Position *int  `json:"position"`
	Excluded *bool `json:"excluded"`
}

// addPathGroupMember appends a path to the group's candidate list. The path
// must exist and face the same direction as the group (F-0266 organizes by
// direction, so a mixed-direction list would be meaningless).
func (h *Handler) addPathGroupMember(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	var in pathGroupMemberInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.PathID == nil || *in.PathID == 0 {
		fail(c, http.StatusBadRequest, errors.New("path_id is required"))
		return
	}
	var p storage.Path
	if err := h.db.First(&p, *in.PathID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("path not found"))
		return
	}
	if p.Direction != g.Direction {
		fail(c, http.StatusBadRequest, errors.New("path direction does not match group direction"))
		return
	}
	m := storage.PathGroupMember{GroupID: g.ID, PathID: *in.PathID}
	if in.Position != nil {
		m.Position = *in.Position
	}
	if in.Excluded != nil {
		m.Excluded = *in.Excluded
	}
	if err := h.db.Create(&m).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("path already in group"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, m)
}

// updatePathGroupMember repositions or (un)excludes a member (F-0265/F-0267).
func (h *Handler) updatePathGroupMember(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	var m storage.PathGroupMember
	if err := h.db.Where("group_id = ? AND path_id = ?", g.ID, c.Param("pathId")).First(&m).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("path is not a member of the group"))
		return
	}
	var in pathGroupMemberInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if in.Position != nil {
		updates["position"] = *in.Position
	}
	if in.Excluded != nil {
		updates["excluded"] = *in.Excluded
	}
	if len(updates) == 0 {
		fail(c, http.StatusBadRequest, errors.New("nothing to update"))
		return
	}
	if err := h.db.Model(&storage.PathGroupMember{}).Where("id = ?", m.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var out storage.PathGroupMember
	if err := h.db.First(&out, m.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// removePathGroupMember drops a path from the candidate list; removing the
// default also clears the group default (F-0264).
func (h *Handler) removePathGroupMember(c *gin.Context) {
	g, err := h.findPathGroup(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("path group not found"))
		return
	}
	res := h.db.Where("group_id = ? AND path_id = ?", g.ID, c.Param("pathId")).Delete(&storage.PathGroupMember{})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("path is not a member of the group"))
		return
	}
	if g.DefaultPathID != nil && *g.DefaultPathID == pathIDParam(c.Param("pathId")) {
		if err := h.db.Model(g).Update("default_path_id", nil).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"removed": true})
}

// pathIDParam parses the :pathId route param; 0 never matches a member.
func pathIDParam(raw string) uint {
	id, _ := strconv.ParseUint(raw, 10, 64)
	return uint(id)
}
