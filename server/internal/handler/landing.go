package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Manual landing assignment (E-20, P0 manual lane):
// dash assigns a landing and weight for an entry node (or a whole region) and persists it as a record; release does not delete the row (the assignment history stays replayable).
// The effective chain that pushes the entry landing list via config.push follows the agent relay config integration (E-5).

// landingInput is the payload for creating an assignment: entry-level (entry_node_id) or region-level (region), exactly one of the two.
type landingInput struct {
	EntryNodeID   *uint  `json:"entry_node_id"`
	Region        string `json:"region"`
	LandingNodeID uint   `json:"landing_node_id"`
	Direction     string `json:"direction"`
	Weight        *int   `json:"weight,omitempty"`
	Reason        string `json:"reason"`
}

func (h *Handler) listLandings(c *gin.Context) {
	q := h.db.Model(&storage.LandingAssignment{})
	if c.Query("scope") != "all" {
		q = q.Where("released_at IS NULL")
	}
	if d := c.Query("direction"); d == "out" || d == "in" {
		q = q.Where("direction = ?", d)
	}
	out := []storage.LandingAssignment{}
	if err := q.Order("id DESC").Limit(500).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createLanding(c *gin.Context) {
	var in landingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.Region = strings.TrimSpace(in.Region)
	in.Reason = strings.TrimSpace(in.Reason)
	if (in.EntryNodeID == nil) == (in.Region == "") {
		fail(c, http.StatusBadRequest, errors.New("指定 entry_node_id 或 region 其一"))
		return
	}
	if in.Direction != "out" && in.Direction != "in" {
		fail(c, http.StatusBadRequest, errors.New("direction must be out/in"))
		return
	}
	weight := 0
	if in.Weight != nil {
		weight = *in.Weight
	}
	if weight < 0 || weight > 1000 {
		fail(c, http.StatusBadRequest, errors.New("weight must be 0-1000"))
		return
	}
	var landing storage.Node
	if err := h.db.First(&landing, in.LandingNodeID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("landing node not found"))
		return
	}
	if landing.Role != "landing" && landing.Role != "both" {
		fail(c, http.StatusBadRequest, errors.New("landing node must have landing role"))
		return
	}
	row := storage.LandingAssignment{
		LandingNodeID: landing.ID,
		Direction:     in.Direction,
		Strategy:      "manual",
		Weight:        weight,
		Reason:        in.Reason,
		AssignedAt:    time.Now(),
	}
	if in.EntryNodeID != nil {
		var entry storage.Node
		if err := h.db.First(&entry, *in.EntryNodeID).Error; err != nil {
			fail(c, http.StatusBadRequest, errors.New("entry node not found"))
			return
		}
		if entry.Role != "entry" && entry.Role != "both" {
			fail(c, http.StatusBadRequest, errors.New("entry node must have entry role"))
			return
		}
		if entry.ID == landing.ID {
			fail(c, http.StatusBadRequest, errors.New("entry and landing must differ"))
			return
		}
		row.EntryNodeID = &entry.ID
	} else {
		row.Region = in.Region
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, row)
}

// updateLanding only allows adjusting the weight (other changes go through a new assignment + releasing the old one for the record).
func (h *Handler) updateLanding(c *gin.Context) {
	row, ok := h.findActiveLanding(c)
	if !ok {
		return
	}
	var in struct {
		Weight *int `json:"weight"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Weight == nil {
		fail(c, http.StatusBadRequest, errors.New("weight is required"))
		return
	}
	if *in.Weight < 0 || *in.Weight > 1000 {
		fail(c, http.StatusBadRequest, errors.New("weight must be 0-1000"))
		return
	}
	if err := h.db.Model(&storage.LandingAssignment{}).Where("id=?", row.ID).
		Update("weight", *in.Weight).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row.Weight = *in.Weight
	c.JSON(http.StatusOK, row)
}

// releaseLanding releases an assignment: sets released_at for the record, no physical delete.
func (h *Handler) releaseLanding(c *gin.Context) {
	row, ok := h.findActiveLanding(c)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	now := time.Now()
	if err := h.db.Model(&storage.LandingAssignment{}).Where("id=?", row.ID).
		Updates(map[string]any{"released_at": now, "release_reason": strings.TrimSpace(in.Reason)}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row.ReleasedAt = &now
	row.ReleaseReason = strings.TrimSpace(in.Reason)
	c.JSON(http.StatusOK, row)
}

func (h *Handler) findActiveLanding(c *gin.Context) (storage.LandingAssignment, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.LandingAssignment{}, false
	}
	var row storage.LandingAssignment
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return storage.LandingAssignment{}, false
		}
		fail(c, http.StatusInternalServerError, err)
		return storage.LandingAssignment{}, false
	}
	if row.ReleasedAt != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "分配已释放"})
		return storage.LandingAssignment{}, false
	}
	return row, true
}
