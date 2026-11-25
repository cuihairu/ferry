package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Admin side of quota linkage (SAVE-6): viewing/setting configuration and the
// downgrade trail. The linkage decision runs periodically in quota.LinkLoop;
// here we only read the results and change the configuration.

// listQuotaLink returns the linkage setting, the downgrade trail (active rows
// first) and the user id to username mapping.
func (h *Handler) listQuotaLink(c *gin.Context) {
	setting, err := quota.LoadLinkSetting(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rows := []storage.QuotaAction{}
	if err := h.db.Order("(released_at IS NULL) DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	names := map[uint]string{}
	for _, row := range rows {
		names[row.UserID] = ""
	}
	if len(names) > 0 {
		ids := make([]uint, 0, len(names))
		for id := range names {
			ids = append(ids, id)
		}
		var users []storage.User
		if err := h.db.Select("id, username").Where("id IN ?", ids).Find(&users).Error; err == nil {
			for _, u := range users {
				names[u.ID] = u.Username
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"setting": setting, "rows": rows, "names": names})
}

// putQuotaLink sets the linkage configuration.
func (h *Handler) putQuotaLink(c *gin.Context) {
	var in quota.LinkSetting
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := quota.SaveLinkSetting(h.db, in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	out, err := quota.LoadLinkSetting(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
