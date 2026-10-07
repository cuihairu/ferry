package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 入口域名数据面（触达批 TOUCH-3，用户触达设计 §5/§6）：域名例行邮件与
// 断联容灾（订阅备用信息、推新入口）的共同数据源。管理侧 CRUD，dash 侧
// 断联态页展示与维护（TOUCH-7）。

var errEntryDomainRole = errors.New("role must be primary/backup")

// listEntryDomains 入口域名清单（GET /api/entry-domains）。
func (h *Handler) listEntryDomains(c *gin.Context) {
	var rows []storage.EntryDomain
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// createEntryDomain 新增（POST /api/entry-domains）：{domain, role, region?}。
func (h *Handler) createEntryDomain(c *gin.Context) {
	var in struct {
		Domain string `json:"domain"`
		Role   string `json:"role"`
		Region string `json:"region"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.Domain = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(in.Domain), "https://"), "http://"))
	in.Domain = strings.TrimSuffix(in.Domain, "/")
	if in.Domain == "" {
		fail(c, http.StatusBadRequest, errors.New("domain 不能为空"))
		return
	}
	if in.Role != "primary" && in.Role != "backup" {
		fail(c, http.StatusBadRequest, errEntryDomainRole)
		return
	}
	row := storage.EntryDomain{
		Domain: in.Domain, Role: in.Role, Region: strings.TrimSpace(in.Region),
		Enabled: true, UpdatedAt: time.Now(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, row)
}

// updateEntryDomain 调整（PUT /api/entry-domains/:id）：部分更新，传啥改啥。
func (h *Handler) updateEntryDomain(c *gin.Context) {
	var row storage.EntryDomain
	if err := h.db.First(&row, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("not found"))
		return
	}
	var in struct {
		Domain  *string `json:"domain"`
		Role    *string `json:"role"`
		Region  *string `json:"region"`
		Enabled *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Domain != nil {
		d := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(*in.Domain), "https://"), "http://"))
		if d == "" {
			fail(c, http.StatusBadRequest, errors.New("domain 不能为空"))
			return
		}
		row.Domain = strings.TrimSuffix(d, "/")
	}
	if in.Role != nil {
		if *in.Role != "primary" && *in.Role != "backup" {
			fail(c, http.StatusBadRequest, errEntryDomainRole)
			return
		}
		row.Role = *in.Role
	}
	if in.Region != nil {
		row.Region = strings.TrimSpace(*in.Region)
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	row.UpdatedAt = time.Now()
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteEntryDomain 删除（DELETE /api/entry-domains/:id）。
func (h *Handler) deleteEntryDomain(c *gin.Context) {
	res := h.db.Delete(&storage.EntryDomain{}, c.Param("id"))
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
