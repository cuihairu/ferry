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

// 用户模板（P1-5，对齐 Marzban user_template 的默认配额/时长口径）：
// 单套默认模板存 settings KV，新建用户 use_template=true 时补全缺省字段。

// settingKeyUserTemplate 是用户模板的 settings 键。
const settingKeyUserTemplate = "user_template"

// userTemplate 是默认模板载荷：quota_bytes=默认配额（0 不限）、
// expire_days=默认时长（0 不限期）、reset_cycle=默认重置周期。
type userTemplate struct {
	QuotaBytes int64  `json:"quota_bytes"`
	ExpireDays int    `json:"expire_days"`
	ResetCycle string `json:"reset_cycle"`
}

// defaultUserTemplate 返回全部缺省的模板（未配置或存量损坏时兜底）。
func defaultUserTemplate() userTemplate {
	return userTemplate{ResetCycle: quota.CycleNone}
}

// loadUserTemplate 读取模板；未配置或存量 JSON 损坏按默认值兜底（不阻断创建）。
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
	return tpl, nil
}

// getUserTemplate 查看默认模板（GET /api/user-template）。
func (h *Handler) getUserTemplate(c *gin.Context) {
	tpl, err := h.loadUserTemplate()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, tpl)
}

// putUserTemplate 保存默认模板（PUT /api/user-template）。
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

// applyUserTemplate 把模板填入创建请求中缺席的字段（显式字段优先）。
// 时长按 now+expire_days 日历天推算，expire_days<=0 表示不限期。
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
}
