package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/geodns"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 域名前置（BR-2）管理端：DNS 商凭证与前置记录。
// 凭证机密走 R24 加密面口径（同云提供商），接口只回 has_api_key；
// 类型经 dns.KnownKind 校验，未知类型录入即拒（不冒称支持）。

type dnsProviderView struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Enabled   bool   `json:"enabled"`
	HasAPIKey bool   `json:"has_api_key"`
}

type dnsProviderInput struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	APIKey  string `json:"api_key"` // 明文仅出现在录入/更换请求里
	Enabled *bool  `json:"enabled,omitempty"`
}

func validateDNSProvider(in dnsProviderInput) error {
	if in.Name == "" || in.Type == "" {
		return errors.New("name and type are required")
	}
	if !dns.KnownKind(in.Type) {
		return errors.New("dns provider type not supported yet (cloudflare only for now)")
	}
	return nil
}

func (h *Handler) listDNSProviders(c *gin.Context) {
	rows := []storage.DNSProvider{}
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]dnsProviderView, 0, len(rows))
	for _, r := range rows {
		out = append(out, dnsProviderView{ID: r.ID, Name: r.Name, Type: r.Type,
			Enabled: r.Enabled, HasAPIKey: r.APIKey != ""})
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createDNSProvider(c *gin.Context) {
	var in dnsProviderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateDNSProvider(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.APIKey == "" {
		fail(c, http.StatusBadRequest, errors.New("api_key is required"))
		return
	}
	sealed, err := h.sealSecret(in.APIKey)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	row := storage.DNSProvider{Name: in.Name, Type: in.Type, APIKey: sealed, Enabled: true}
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, dnsProviderView{ID: row.ID, Name: row.Name, Type: row.Type,
		Enabled: row.Enabled, HasAPIKey: true})
}

// updateDNSProvider 更新凭证：api_key 非空表示更换机密，空表示保留原值。
func (h *Handler) updateDNSProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns provider not found"})
		return
	}
	var row storage.DNSProvider
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns provider not found"})
		return
	}
	var in dnsProviderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateDNSProvider(in); err != nil {
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
	if in.APIKey != "" {
		sealed, err := h.sealSecret(in.APIKey)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		row.APIKey = sealed
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, dnsProviderView{ID: row.ID, Name: row.Name, Type: row.Type,
		Enabled: row.Enabled, HasAPIKey: row.APIKey != ""})
}

func (h *Handler) deleteDNSProvider(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns provider not found"})
		return
	}
	var n int64
	h.db.Model(&storage.DNSFront{}).Where("provider_id = ?", id).Count(&n)
	if n > 0 {
		fail(c, http.StatusConflict, errors.New("provider has dns fronts, remove them first"))
		return
	}
	if err := h.db.Delete(&storage.DNSProvider{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- 前置记录 ----

type dnsFrontInput struct {
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	ProviderID *uint  `json:"provider_id"`
	PrimaryIP  string `json:"primary_ip"`
	BackupIPs  string `json:"backup_ips"` // JSON 字符串数组
}

func validateDNSFront(in dnsFrontInput) error {
	if strings.TrimSpace(in.Domain) == "" {
		return errors.New("domain is required")
	}
	if !strings.Contains(in.Domain, ".") {
		return errors.New("domain must be a FQDN (e.g. edge.example.com)")
	}
	if in.ProviderID == nil || *in.ProviderID == 0 {
		return errors.New("provider_id is required")
	}
	if strings.TrimSpace(in.PrimaryIP) == "" {
		return errors.New("primary_ip is required")
	}
	if strings.TrimSpace(in.BackupIPs) == "" {
		return errors.New("backup_ips is required (JSON string array)")
	}
	var ips []string
	if err := json.Unmarshal([]byte(in.BackupIPs), &ips); err != nil {
		return errors.New("backup_ips must be a JSON string array")
	}
	nonempty := false
	for _, ip := range ips {
		if strings.TrimSpace(ip) != "" {
			nonempty = true
			break
		}
	}
	if !nonempty {
		return errors.New("backup_ips must contain at least one IP")
	}
	return nil
}

func (h *Handler) listDNSFronts(c *gin.Context) {
	out := []storage.DNSFront{}
	if err := h.db.Order("id ASC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createDNSFront(c *gin.Context) {
	var in dnsFrontInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateDNSFront(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var prov storage.DNSProvider
	if err := h.db.First(&prov, *in.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("dns provider not found"))
		return
	}
	row := storage.DNSFront{Name: in.Name, Domain: in.Domain, ProviderID: *in.ProviderID,
		PrimaryIP: in.PrimaryIP, BackupIPs: in.BackupIPs}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) updateDNSFront(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns front not found"})
		return
	}
	var row storage.DNSFront
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns front not found"})
		return
	}
	var in dnsFrontInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateDNSFront(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var prov storage.DNSProvider
	if err := h.db.First(&prov, *in.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("dns provider not found"))
		return
	}
	row.Name = in.Name
	row.Domain = in.Domain
	row.ProviderID = *in.ProviderID
	row.PrimaryIP = in.PrimaryIP
	row.BackupIPs = in.BackupIPs
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteDNSFront 删除前置记录；切离中（switched）拒绝——先等回切或人工恢复，
// 避免删掉后域名悬在备用 IP 上无人回切。
func (h *Handler) deleteDNSFront(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns front not found"})
		return
	}
	var row storage.DNSFront
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dns front not found"})
		return
	}
	if row.Switched {
		fail(c, http.StatusConflict, errors.New("front is switched off primary, restore it first"))
		return
	}
	if err := h.db.Delete(&storage.DNSFront{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// geoSync 手动分地域对账（POST /api/dns-fronts/geo-sync，E-27 设计 §C.7
// 补偿入口）：事件驱动同步的兜底——自动钩子（池摘挂）失败或人工改库后，
// 管理员可在此补一轮对齐；changed 为本次实际变更记录数。
func (h *Handler) geoSync(c *gin.Context) {
	s := &geodns.Syncer{DB: h.db, Store: h.secrets}
	n, err := s.Sync(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"changed": n})
}
