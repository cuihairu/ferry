package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 断联容灾（触达批 TOUCH-7，用户触达设计 §4）：面板域名被封或面板不可达时
// 用户仍能找到我们。判定源=管理员手动标记断联态（「复用边缘探测结论」不可
// 执行：ProbeReport 只测隧道/出口/节点自身，没有面板入口域名的探测面）。
// 逃生通道=订阅文本注释里带备用公告地址与备用域名清单——客户端缓存的订阅
// 里自带，断联前就已下发；推新入口走既有公告扇出（站内信+notice 事件经
// Herald 分发），不另起机制。

// outageSettingKey 断联态开关的设置键：缺省常态，"1" 断联。
const outageSettingKey = "outage_mode"

// outageMode 读断联态标记；未设置按常态。
func outageMode(db *gorm.DB) bool {
	v, ok, err := storage.GetSetting(db, outageSettingKey)
	if err != nil || !ok {
		return false
	}
	return v == "1" || v == "true"
}

// getOutage 断联态回读（GET /api/outage）。
func (h *Handler) getOutage(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"enabled": outageMode(h.db)})
}

// putOutage 断联态标记（PUT /api/outage {enabled}）：面板域名被封或人工
// 确认不可达时由管理员打开；订阅注释随之加警告行，恢复后关闭。
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

// outageNotesCap 备用域名注释的条数上限，防域名清单过长撑爆订阅文本。
const outageNotesCap = 8

// outageNotes 组装订阅响应的备用信息注释（TOUCH-7）：公告订阅地址（断联后
// 从阅读器拿新入口）+ 启用的入口域名清单（主备标注）+ 断联态警告。常附——
// 逃生通道要在断联前就进客户端缓存，只有警告行由断联态开关控制。
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
