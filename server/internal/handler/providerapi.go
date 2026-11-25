package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Provisioning configuration (OS-1): cloud provider credentials and instance templates.
// Credential secrets go through the single secret entry point and are stored
// encrypted with AES-256-GCM (R24 wording): entry is rejected while the master
// key is unconfigured; the API layer only returns has_access_key and never echoes
// the plaintext.
//
// Least-privilege guidance for the cloud accounts behind these credentials
// (which vendor IAM actions Ferry actually needs, per vendor) lives in
// docs/design/cloud-account-least-privilege.md (F-0335).

// providerView is a provider's outward view: secret fields are presented in masked form.
type providerView struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	HasAccessKey bool   `json:"has_access_key"`
	Note         string `json:"note"`   // F-0323 account remark
	Region       string `json:"region"` // F-0324 account scope
	// Rotation state (F-0330): the version counter and whether a previous
	// key is still held in the grace window.
	KeyVersion  int        `json:"key_version"`
	PrevKeyHeld bool       `json:"prev_key_held"`
	PrevSetAt   *time.Time `json:"prev_set_at,omitempty"`
}

type providerInput struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	AccessKey string  `json:"access_key"` // plaintext only ever appears in create/replace requests
	Enabled   *bool   `json:"enabled,omitempty"`
	Note      *string `json:"note,omitempty"`   // F-0323; absent keeps the current value on update
	Region    *string `json:"region,omitempty"` // F-0324; absent keeps the current value on update
}

// providerViewOf builds the masked outward view from a row.
func providerViewOf(r storage.Provider) providerView {
	return providerView{
		ID: r.ID, Name: r.Name, Type: r.Type, Enabled: r.Enabled,
		HasAccessKey: r.AccessKey != "", Note: r.Note, Region: r.Region,
		KeyVersion: r.KeyVersion, PrevKeyHeld: r.PrevAccessKey != "", PrevSetAt: r.PrevSetAt,
	}
}

func (h *Handler) listProviders(c *gin.Context) {
	rows := []storage.Provider{}
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]providerView, 0, len(rows))
	for _, r := range rows {
		out = append(out, providerViewOf(r))
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
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	row := storage.Provider{Name: in.Name, Type: in.Type, AccessKey: sealed, Enabled: enabled, KeyVersion: 1}
	if in.Note != nil {
		row.Note = *in.Note
	}
	if in.Region != nil {
		row.Region = *in.Region
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !enabled {
		// Enabled carries default:true, so the zero value false is skipped by GORM and
		// the DB default wins; it has to be written explicitly.
		// After Create the struct is back-filled with the DB default true
		// (RETURNING), so the decision is based on the request input.
		if err := h.db.Model(&storage.Provider{}).Where("id=?", row.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		row.Enabled = false
	}
	c.JSON(http.StatusOK, providerViewOf(row))
}

// updateProvider updates a provider: a non-empty access_key replaces the secret, empty keeps the current value.
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
		// Plain replace (not a rotation, F-0330): no grace copy is kept.
		row.AccessKey = sealed
		row.KeyVersion++
		row.PrevAccessKey = ""
		row.PrevSetAt = nil
	}
	// Absent note/region keep their current values (F-0323/F-0324).
	if in.Note != nil {
		row.Note = *in.Note
	}
	if in.Region != nil {
		row.Region = *in.Region
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, providerViewOf(row))
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

// testProviderConnection verifies an account's credential (F-0328). Every
// account is checked locally first (the ciphertext must decrypt with the
// master key); when a cloudprov adapter is registered for the provider type
// the check goes further and probes the vendor API through the shared client
// policy (timeout/rate-limit/retry). The plaintext key never leaves the
// process and is never logged. Test outcomes are 200 with an ok flag; only
// unknown/disabled accounts are 4xx.
func (h *Handler) testProviderConnection(c *gin.Context) {
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
	if !row.Enabled {
		fail(c, http.StatusConflict, errors.New("provider disabled"))
		return
	}
	plain, err := h.secrets.Decrypt(row.AccessKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "mode": "local",
			"error": "credential unreadable: " + err.Error()})
		return
	}
	factory := cloudprov.Lookup(row.Type)
	if factory == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "mode": "local",
			"message": "credential readable; no adapter registered for type " + row.Type})
		return
	}
	a, err := factory(cloudprov.Account{
		ProviderID: row.ID, Name: row.Name, Type: row.Type,
		AccessKey: plain, Region: row.Region,
	})
	if err != nil {
		e := cloudprov.AsError(err)
		c.JSON(http.StatusOK, gin.H{"ok": false, "mode": "adapter",
			"error_type": e.Type, "error": e.Error()})
		return
	}
	caps, err := cloudprov.NewClient(a, cloudprov.Defaults).Capabilities(c.Request.Context())
	if err != nil {
		e := cloudprov.AsError(err)
		c.JSON(http.StatusOK, gin.H{"ok": false, "mode": "adapter",
			"error_type": e.Type, "error": e.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "mode": "adapter",
		"api_version": caps.APIVersion, "capabilities": caps})
}

// RotationGrace is how long the previous credential stays in the grace
// window after a rotation (F-0330): provision jobs started with the old key
// can finish while new work uses the new key. Vendors must allow two live
// keys for this window; revoking early is an explicit second step.
const RotationGrace = 24 * time.Hour

// rotateProviderCredential rotates a provider's credential with a grace
// window (F-0330): the current ciphertext moves to PrevAccessKey, the new
// plaintext is sealed into AccessKey, KeyVersion bumps, and PrevSetAt opens
// the grace window. Supply always uses AccessKey (the active key); the
// previous key is retained only for certification until revoke.
func (h *Handler) rotateProviderCredential(c *gin.Context) {
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
	var in struct {
		AccessKey string `json:"access_key"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.AccessKey == "" {
		fail(c, http.StatusBadRequest, errors.New("access_key is required"))
		return
	}
	sealed, err := h.sealSecret(in.AccessKey)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	updates := map[string]any{
		"prev_access_key": row.AccessKey,
		"access_key":      sealed,
		"key_version":     row.KeyVersion + 1,
		"prev_set_at":     now,
	}
	if err := h.db.Model(&storage.Provider{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.First(&row, row.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, providerViewOf(row))
}

// revokePreviousCredential closes the grace window early (F-0330): the
// previous ciphertext is dropped so only the active key remains. Supply and
// connection tests never read it; revocation is a distinct, audited write.
func (h *Handler) revokePreviousCredential(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
		return
	}
	res := h.db.Model(&storage.Provider{}).Where("id = ? AND prev_access_key <> ''", id).
		Updates(map[string]any{"prev_access_key": "", "prev_set_at": nil})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no previous credential to revoke"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// sealSecret encrypts a secret; it returns an actionable error when the master key is not configured.
func (h *Handler) sealSecret(plain string) (string, error) {
	if !h.secrets.Enabled() {
		return "", errors.New("secret master key not configured (set FERRY_SECRET_KEY)")
	}
	return h.secrets.Encrypt(plain)
}

// ---- instance templates (OS-1) ----

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
	Config            string `json:"config"` // protocol config template JSON, optional (OS-4 writes it to the node row at provisioning)
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
