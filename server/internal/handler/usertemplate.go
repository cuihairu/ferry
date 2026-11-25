package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// User template (P1-5, aligned with Marzban user_template's default
// quota/duration terms): a single default template stored in the settings KV;
// creating a user with use_template=true fills in absent fields.

// settingKeyUserTemplate is the settings key for the user template.
const settingKeyUserTemplate = "user_template"

// userTemplate is the default template payload: quota_bytes=default quota
// (0 unlimited), expire_days=default duration (0 no expiry),
// reset_cycle=default reset cycle.
type userTemplate struct {
	QuotaBytes int64  `json:"quota_bytes"`
	ExpireDays int    `json:"expire_days"`
	ResetCycle string `json:"reset_cycle"`
	// BwUpMbps/BwDownMbps default per-user bandwidth limits (P2-3, 0=unlimited).
	BwUpMbps   int `json:"bw_up_mbps"`
	BwDownMbps int `json:"bw_down_mbps"`
}

// defaultUserTemplate returns the all-defaults template (fallback when
// unconfigured or when stored data is corrupt).
func defaultUserTemplate() userTemplate {
	return userTemplate{ResetCycle: quota.CycleNone}
}

// loadUserTemplate reads the template; unconfigured or corrupt stored JSON
// falls back to defaults (never blocks creation).
func (h *Handler) loadUserTemplate() (userTemplate, error) {
	tpl := defaultUserTemplate()
	raw, ok, err := storage.GetSetting(h.db, settingKeyUserTemplate)
	if err != nil || !ok {
		return tpl, err
	}
	if err := json.Unmarshal([]byte(raw), &tpl); err != nil {
		return defaultUserTemplate(), nil
	}
	if tpl.ResetCycle == "" {
		tpl.ResetCycle = quota.CycleNone
	}
	if err := validateUserBw(&tpl.BwUpMbps, &tpl.BwDownMbps); err != nil {
		return defaultUserTemplate(), nil
	}
	return tpl, nil
}

// validateUserBw validates per-user bandwidth limits (P2-3): 0=unlimited is
// allowed; negatives and values over 100000Mbps are treated as dirty and
// rejected (so a mistyped entry cannot blow out the node bandwidth).
func validateUserBw(up, down *int) error {
	if derefInt(up) < 0 || derefInt(down) < 0 || derefInt(up) > 100000 || derefInt(down) > 100000 {
		return errors.New("bw_up_mbps/bw_down_mbps must be 0-100000 (0 = unlimited)")
	}
	return nil
}

// derefInt dereferences, with nil mapping to 0 (= unlimited).
func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// getUserTemplate views the default template (GET /api/user-template).
func (h *Handler) getUserTemplate(c *gin.Context) {
	tpl, err := h.loadUserTemplate()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, tpl)
}

// putUserTemplate saves the default template (PUT /api/user-template).
func (h *Handler) putUserTemplate(c *gin.Context) {
	var in userTemplate
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.QuotaBytes < 0 {
		fail(c, http.StatusBadRequest, errors.New("quota_bytes must be >= 0 (0 = unlimited)"))
		return
	}
	if in.ExpireDays < 0 {
		fail(c, http.StatusBadRequest, errors.New("expire_days must be >= 0 (0 = no expiry)"))
		return
	}
	if in.ResetCycle == "" {
		in.ResetCycle = quota.CycleNone
	}
	if !quota.ValidCycle(in.ResetCycle) {
		fail(c, http.StatusBadRequest, errors.New("reset_cycle must be none/day/week/month"))
		return
	}
	data, err := json.Marshal(in)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, settingKeyUserTemplate, string(data)); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, in)
}

// applyUserTemplate fills template values into the create request's absent
// fields (explicit fields win). Duration is derived as now+expire_days
// calendar days; expire_days<=0 means no expiry.
func applyUserTemplate(in *model.UserInput, tpl userTemplate, now time.Time) {
	if in.QuotaBytes == nil {
		q := tpl.QuotaBytes
		in.QuotaBytes = &q
	}
	if in.ResetCycle == nil || *in.ResetCycle == "" {
		c := tpl.ResetCycle
		in.ResetCycle = &c
	}
	if in.ExpiresAt == nil && tpl.ExpireDays > 0 {
		t := now.AddDate(0, 0, tpl.ExpireDays)
		in.ExpiresAt = &t
	}
	// Bandwidth limits (P2-3): only a positive template value fills the
	// default (0=unlimited must not override an explicit 0).
	if in.BwUpMbps == nil && tpl.BwUpMbps > 0 {
		v := tpl.BwUpMbps
		in.BwUpMbps = &v
	}
	if in.BwDownMbps == nil && tpl.BwDownMbps > 0 {
		v := tpl.BwDownMbps
		in.BwDownMbps = &v
	}
}
