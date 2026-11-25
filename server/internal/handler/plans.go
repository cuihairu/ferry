package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Plan management (F-0101): the admin keeps a set of sellable package
// templates and applies them to users in bulk. The fields mirror the user
// surface's limit columns, so apply is a straight copy plus optional expiry.

// planInput is the create/update payload; every limit field follows the user
// convention (0 = unlimited / unchanged).
type planInput struct {
	Name        string  `json:"name"`
	Note        *string `json:"note"`
	Enabled     *bool   `json:"enabled"`
	QuotaBytes  *int64  `json:"quota_bytes"`
	ResetCycle  string  `json:"reset_cycle"`
	ExpiresDays *int    `json:"expires_days"`
	BwUpMbps    *int    `json:"bw_up_mbps"`
	BwDownMbps  *int    `json:"bw_down_mbps"`
	DeviceLimit *int    `json:"device_limit"`
	IPLimit     *int    `json:"ip_limit"`
}

// validatePlanInput runs the shared user-limit validations and fills defaults;
// the returned row carries the normalized values.
func validatePlanInput(in planInput) (storage.Plan, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return storage.Plan{}, errors.New("name is required (1-64 chars)")
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		return storage.Plan{}, errors.New("quota_bytes must be >= 0 (0 = unlimited)")
	}
	if err := validateUserBw(in.BwUpMbps, in.BwDownMbps); err != nil {
		return storage.Plan{}, err
	}
	if err := validateSubLimit(in.DeviceLimit, in.IPLimit); err != nil {
		return storage.Plan{}, err
	}
	cycle := "none"
	if in.ResetCycle != "" {
		c, err := resetCycleOrDefault(&in.ResetCycle)
		if err != nil {
			return storage.Plan{}, err
		}
		cycle = c
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return storage.Plan{
		Name:        name,
		Enabled:     enabled,
		QuotaBytes:  derefInt64(in.QuotaBytes),
		ResetCycle:  cycle,
		ExpiresDays: derefInt(in.ExpiresDays),
		BwUpMbps:    derefInt(in.BwUpMbps),
		BwDownMbps:  derefInt(in.BwDownMbps),
		DeviceLimit: derefInt(in.DeviceLimit),
		IPLimit:     derefInt(in.IPLimit),
	}, nil
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func (h *Handler) listPlans(c *gin.Context) {
	var plans []storage.Plan
	if err := h.db.Order("id DESC").Find(&plans).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"plans": plans})
}

func (h *Handler) getPlan(c *gin.Context) {
	p, err := h.findPlan(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("plan not found"))
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *Handler) createPlan(c *gin.Context) {
	var in planInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p, err := validatePlanInput(in)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Note != nil {
		p.Note = *in.Note
	}
	if err := h.db.Create(&p).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("plan name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !p.Enabled {
		// Enabled carries default:true, so an explicit false needs a direct write.
		if err := h.db.Model(&storage.Plan{}).Where("id=?", p.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) findPlan(id string) (*storage.Plan, error) {
	var p storage.Plan
	if err := h.db.First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (h *Handler) updatePlan(c *gin.Context) {
	p, err := h.findPlan(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("plan not found"))
		return
	}
	var in planInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// Update validates against the merged row: absent fields keep their values.
	merged := planInput{
		Name: in.Name, Note: in.Note, Enabled: in.Enabled,
		QuotaBytes: in.QuotaBytes, ResetCycle: in.ResetCycle,
		ExpiresDays: in.ExpiresDays, BwUpMbps: in.BwUpMbps, BwDownMbps: in.BwDownMbps,
		DeviceLimit: in.DeviceLimit, IPLimit: in.IPLimit,
	}
	if merged.Name == "" {
		merged.Name = p.Name
	}
	if merged.ResetCycle == "" {
		merged.ResetCycle = p.ResetCycle
	}
	if merged.QuotaBytes == nil {
		v := p.QuotaBytes
		merged.QuotaBytes = &v
	}
	if merged.ExpiresDays == nil {
		v := p.ExpiresDays
		merged.ExpiresDays = &v
	}
	if merged.BwUpMbps == nil {
		v := p.BwUpMbps
		merged.BwUpMbps = &v
	}
	if merged.BwDownMbps == nil {
		v := p.BwDownMbps
		merged.BwDownMbps = &v
	}
	if merged.DeviceLimit == nil {
		v := p.DeviceLimit
		merged.DeviceLimit = &v
	}
	if merged.IPLimit == nil {
		v := p.IPLimit
		merged.IPLimit = &v
	}
	if merged.Enabled == nil {
		v := p.Enabled
		merged.Enabled = &v
	}
	next, err := validatePlanInput(merged)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Note != nil {
		next.Note = *in.Note
	} else {
		next.Note = p.Note
	}
	next.ID = p.ID
	next.CreatedAt = p.CreatedAt
	if err := h.db.Model(&storage.Plan{}).Where("id = ?", p.ID).Select("name", "note", "enabled",
		"quota_bytes", "reset_cycle", "expires_days", "bw_up_mbps", "bw_down_mbps", "device_limit", "ip_limit", "updated_at").
		Updates(&next).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("plan name already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out, err := h.findPlan(c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) deletePlan(c *gin.Context) {
	res := h.db.Delete(&storage.Plan{}, c.Param("id"))
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("plan not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// applyPlan copies the plan's limits onto the listed users (F-0044: partial
// failure is per row, never a blanket success). ExpiresDays>0 also rewrites
// each user's ExpiresAt relative to now; 0 leaves expiry untouched.
func (h *Handler) applyPlan(c *gin.Context) {
	p, err := h.findPlan(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("plan not found"))
		return
	}
	if !p.Enabled {
		fail(c, http.StatusConflict, errors.New("plan is disabled"))
		return
	}
	var in struct {
		UserIDs []uint `json:"user_ids"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if len(in.UserIDs) == 0 {
		fail(c, http.StatusBadRequest, errors.New("user_ids is required"))
		return
	}
	now := time.Now()
	var expiresAt *time.Time
	if p.ExpiresDays > 0 {
		exp := now.AddDate(0, 0, p.ExpiresDays)
		expiresAt = &exp
	}
	out := make([]gin.H, 0, len(in.UserIDs))
	for _, uid := range in.UserIDs {
		updates := map[string]any{
			"quota_bytes":  p.QuotaBytes,
			"reset_cycle":  p.ResetCycle,
			"bw_up_mbps":   p.BwUpMbps,
			"bw_down_mbps": p.BwDownMbps,
			"device_limit": p.DeviceLimit,
			"ip_limit":     p.IPLimit,
			"updated_at":   now,
		}
		if expiresAt != nil {
			updates["expires_at"] = expiresAt
		}
		res := h.db.Model(&storage.User{}).Where("id = ?", uid).Updates(updates)
		row := gin.H{"user_id": uid, "ok": res.Error == nil && res.RowsAffected > 0}
		if res.Error != nil {
			row["error"] = res.Error.Error()
		} else if res.RowsAffected == 0 {
			row["error"] = "user not found"
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"applied": len(out), "results": out})
}
