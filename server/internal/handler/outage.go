package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Outage resilience (reachability batch TOUCH-7, user-reachability design §4):
// when the panel domain is blocked or the panel is unreachable, users can still
// find us. The decision source is an admin-set outage flag ("reuse edge probe
// results" is not executable: ProbeReport only probes the tunnel/egress/node
// itself and has no probe surface for the panel entry domain).
// The escape hatch = subscription text notes carrying the backup announcement
// address and the backup domain list - they come bundled in the subscription the
// client caches, delivered before the outage; pushing a new entry point reuses
// the existing announcement fan-out (in-app message + notice event via
// Herald), no separate mechanism.

// outageSettingKey is the setting key for the outage flag: normal state by
// default, "1" means outage.
const outageSettingKey = "outage_mode"

// outageMode reads the outage flag; unset counts as normal state.
func outageMode(db *gorm.DB) bool {
	v, ok, err := storage.GetSetting(db, outageSettingKey)
	if err != nil || !ok {
		return false
	}
	return v == "1" || v == "true"
}

// getOutage reads back the outage flag (GET /api/outage).
func (h *Handler) getOutage(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"enabled": outageMode(h.db)})
}

// putOutage sets the outage flag (PUT /api/outage {enabled}): the admin turns
// it on when the panel domain is blocked or reachability is manually confirmed
// lost; a warning line is added to the subscription notes and it is turned off
// again on recovery.
func (h *Handler) putOutage(c *gin.Context) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Enabled == nil {
		fail(c, http.StatusBadRequest, errors.New("enabled is required"))
		return
	}
	v := "0"
	if *in.Enabled {
		v = "1"
	}
	if err := storage.SetSetting(h.db, outageSettingKey, v); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": *in.Enabled})
}

// outageNotesCap is the cap on the number of backup domain notes, to stop an
// overlong domain list from bloating the subscription text.
const outageNotesCap = 8

// outageNotes assembles the backup info notes for the subscription response
// (TOUCH-7): the announcement subscription address (where to fetch a new entry
// point after the outage) + the enabled entry domain list (primary/backup
// marks) + the outage warning. It is always attached - the escape hatch must
// land in the client cache before the outage, only the warning line is
// controlled by the outage flag.
func (h *Handler) outageNotes() []string {
	var notes []string
	if base := strings.TrimRight(h.cfg.BaseURL, "/"); base != "" {
		notes = append(notes, "公告订阅（面板失联时从这里获取新入口）: "+base+"/feed.xml")
	}
	var domains []storage.EntryDomain
	if err := h.db.Where("enabled = ?", true).Order("id").Limit(outageNotesCap).Find(&domains).Error; err == nil && len(domains) > 0 {
		parts := make([]string, 0, len(domains))
		for _, d := range domains {
			mark := "备"
			if d.Role == "primary" {
				mark = "主"
			}
			parts = append(parts, d.Domain+"("+mark+")")
		}
		notes = append(notes, "备用入口域名: "+strings.Join(parts, " "))
	}
	if outageMode(h.db) {
		notes = append(notes, "警告：面板入口处于断联态，请改用备用域名并从公告订阅获取最新入口")
	}
	return notes
}
