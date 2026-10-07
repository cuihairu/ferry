package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 供给配置（OS-1）：云提供商凭证与机型模板。
// 凭证机密走 secret 统一入口 AES-256-GCM 加密落库（R24 口径）：
// 主密钥未配置时拒绝录入；接口层只回 has_access_key，不回显明文。

// providerView 是提供商的对外视图：机密字段以掩码口径呈现。
type providerView struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	HasAccessKey bool   `json:"has_access_key"`
}

type providerInput struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	AccessKey string `json:"access_key"` // 明文仅出现在录入/更换请求里
	Enabled   *bool  `json:"enabled,omitempty"`
}

func (h *Handler) listProviders(c *gin.Context) {
	rows := []storage.Provider{}
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]providerView, 0, len(rows))
	for _, r := range rows {
		out = append(out, providerView{
			ID: r.ID, Name: r.Name, Type: r.Type, Enabled: r.Enabled,
			HasAccessKey: r.AccessKey != "",
		})
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createProvider(c *gin.Context) {
	var in providerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" || in.Type == "" || in.AccessKey == "" {
		fail(c, http.StatusBadRequest, errors.New("name, type and access_key are required"))
		return
	}
	sealed, err := h.sealSecret(in.AccessKey)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row := storage.Provider{Name: in.Name, Type: in.Type, AccessKey: sealed, Enabled: true}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, providerView{ID: row.ID, Name: row.Name, Type: row.Type,
		Enabled: row.Enabled, HasAccessKey: true})
}

// updateProvider 更新提供商：access_key 非空表示更换机密，空表示保留原值。
func (h *Handler) updateProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	var row storage.Provider
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	var in providerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Name != "" {
		row.Name = in.Name
	}
	if in.Type != "" {
		row.Type = in.Type
	}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if in.AccessKey != "" {
		sealed, err := h.sealSecret(in.AccessKey)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		row.AccessKey = sealed
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, providerView{ID: row.ID, Name: row.Name, Type: row.Type,
		Enabled: row.Enabled, HasAccessKey: row.AccessKey != ""})
}

func (h *Handler) deleteProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	var n int64
	h.db.Model(&storage.ProvisionTemplate{}).Where("provider_id = ?", id).Count(&n)
	if n > 0 {
		fail(c, http.StatusConflict, errors.New("provider has templates, remove them first"))
		return
	}
	if err := h.db.Delete(&storage.Provider{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// sealSecret 加密机密；主密钥未配置时给出可操作的错误信息。
func (h *Handler) sealSecret(plain string) (string, error) {
	if !h.secrets.Enabled() {
		return "", errors.New("secret master key not configured (set FERRY_SECRET_KEY)")
	}
	return h.secrets.Encrypt(plain)
}

// ---- 机型模板（OS-1）----

type templateInput struct {
	Name              string `json:"name"`
	ProviderID        *uint  `json:"provider_id"`
	Plan              string `json:"plan"`
	Region            string `json:"region"`
	BwMbps            *int   `json:"bw_mbps,omitempty"`
	BillingType       string `json:"billing_type"`
	MonthlyCostCents  *int64 `json:"monthly_cost_cents,omitempty"`
	TrafficPriceCents *int64 `json:"traffic_price_cents,omitempty"`
	Direction         string `json:"direction"`
	LineType          string `json:"line_type"`
	Role              string `json:"role"`
	Transport         string `json:"transport"`
	Config            string `json:"config"` // 协议配置模板 JSON，可选（OS-4 随开服落到节点行）
}

func validateTemplate(in templateInput) error {
	if in.Name == "" {
		return errors.New("name is required")
	}
	if in.ProviderID == nil || *in.ProviderID == 0 {
		return errors.New("provider_id is required")
	}
	switch in.Direction {
	case "", "out", "in", "both":
	default:
		return errors.New("direction must be out/in/both")
	}
	switch in.Role {
	case "", "entry", "landing", "both", "relay":
	default:
		return errors.New("role must be entry/landing/both/relay")
	}
	switch in.BillingType {
	case "", "包月", "按流量":
	default:
		return errors.New("billing_type must be 包月/按流量")
	}
	if strings.TrimSpace(in.Config) != "" && !json.Valid([]byte(in.Config)) {
		return errors.New("config must be valid JSON")
	}
	return nil
}

func (h *Handler) listTemplates(c *gin.Context) {
	out := []storage.ProvisionTemplate{}
	if err := h.db.Order("id ASC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createTemplate(c *gin.Context) {
	var in templateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateTemplate(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row := storage.ProvisionTemplate{
		Name: in.Name, ProviderID: *in.ProviderID, Plan: in.Plan, Region: in.Region,
		BillingType: in.BillingType, Direction: in.Direction, LineType: in.LineType,
		Role: in.Role, Transport: in.Transport, Config: normalizeConfig(in.Config),
	}
	if in.BwMbps != nil {
		row.BwMbps = *in.BwMbps
	}
	if in.MonthlyCostCents != nil {
		row.MonthlyCostCents = *in.MonthlyCostCents
	}
	if in.TrafficPriceCents != nil {
		row.TrafficPriceCents = *in.TrafficPriceCents
	}
	if row.Direction == "" {
		row.Direction = "out"
	}
	if row.Role == "" {
		row.Role = "entry"
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) updateTemplate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var row storage.ProvisionTemplate
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var in templateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateTemplate(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row.Name = in.Name
	row.ProviderID = *in.ProviderID
	row.Plan = in.Plan
	row.Region = in.Region
	row.BillingType = in.BillingType
	row.Direction = in.Direction
	row.LineType = in.LineType
	row.Role = in.Role
	row.Transport = in.Transport
	row.Config = normalizeConfig(in.Config)
	if in.BwMbps != nil {
		row.BwMbps = *in.BwMbps
	}
	if in.MonthlyCostCents != nil {
		row.MonthlyCostCents = *in.MonthlyCostCents
	}
	if in.TrafficPriceCents != nil {
		row.TrafficPriceCents = *in.TrafficPriceCents
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) deleteTemplate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err := h.db.Delete(&storage.ProvisionTemplate{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
