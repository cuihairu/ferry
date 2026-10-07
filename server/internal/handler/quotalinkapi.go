package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 配额联动（SAVE-6）管理端：配置查看/设置与降档留痕。
// 联动判定由 quota.LinkLoop 周期执行，这里只读结果与改配置。

// listQuotaLink 返回联动配置、降档留痕（生效中在前）与用户名映射。
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

// putQuotaLink 设置联动配置。
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
