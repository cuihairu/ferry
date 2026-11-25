package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/cert"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Admin side for cert tasks (BR-4): CRUD plus manually triggered issuance.
// Issuance/renewal runs inside the orchestration loop and the trigger
// goroutine (a single acme.sh run takes seconds to minutes); the task row only
// keeps state and expiry.

type certTaskInput struct {
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	Sans          string `json:"sans"`
	Method        string `json:"method"`
	DNSProviderID *uint  `json:"provider_id"`
	CA            string `json:"ca"`
}

func validateCertTask(in certTaskInput) error {
	if strings.TrimSpace(in.Domain) == "" {
		return errors.New("domain is required")
	}
	if !strings.Contains(in.Domain, ".") {
		return errors.New("domain must be a FQDN (e.g. edge.example.com)")
	}
	switch in.Method {
	case "", cert.MethodDNS01, cert.MethodHTTP01:
	default:
		return errors.New("method must be dns-01/http-01")
	}
	if in.Method == cert.MethodDNS01 && (in.DNSProviderID == nil || *in.DNSProviderID == 0) {
		return errors.New("dns-01 requires provider_id (DNS credential)")
	}
	return nil
}

func (h *Handler) listCertTasks(c *gin.Context) {
	out := []storage.CertTask{}
	if err := h.db.Order("id ASC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createCertTask(c *gin.Context) {
	var in certTaskInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateCertTask(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Method == "" {
		in.Method = cert.MethodDNS01
	}
	var providerID uint
	if in.DNSProviderID != nil {
		var prov storage.DNSProvider
		if err := h.db.First(&prov, *in.DNSProviderID).Error; err != nil {
			fail(c, http.StatusBadRequest, errors.New("dns provider not found"))
			return
		}
		providerID = *in.DNSProviderID
	}
	row := storage.CertTask{Name: in.Name, Domain: in.Domain, Sans: in.Sans,
		Method: in.Method, DNSProviderID: providerID, CA: in.CA, State: cert.StatePending}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) updateCertTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cert task not found"})
		return
	}
	var row storage.CertTask
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cert task not found"})
		return
	}
	var in certTaskInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateCertTask(in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var providerID uint
	if in.DNSProviderID != nil {
		var prov storage.DNSProvider
		if err := h.db.First(&prov, *in.DNSProviderID).Error; err != nil {
			fail(c, http.StatusBadRequest, errors.New("dns provider not found"))
			return
		}
		providerID = *in.DNSProviderID
	}
	row.Name = in.Name
	row.Domain = in.Domain
	row.Sans = in.Sans
	row.Method = in.Method
	if in.Method == "" {
		row.Method = cert.MethodDNS01
	}
	row.DNSProviderID = providerID
	row.CA = in.CA
	// After orchestration parameters change, reset to pending so the loop re-issues automatically.
	if row.State != cert.StatePending {
		row.State = cert.StatePending
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) deleteCertTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cert task not found"})
		return
	}
	if err := h.db.Delete(&storage.CertTask{}, id).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// issueCertTask manually triggers issuance (POST /api/cert-tasks/:id/issue):
// it runs asynchronously and the task row returns issuing immediately; 409 =
// already in progress.
func (h *Handler) issueCertTask(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cert task not found"})
		return
	}
	var row storage.CertTask
	if err := h.db.First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "cert task not found"})
		return
	}
	// Claiming issuing prevents double issuance (the loop's table scan skips rows already issuing).
	res := h.db.Model(&storage.CertTask{}).
		Where("id = ? AND state != ?", row.ID, "issuing").
		Update("state", "issuing")
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusConflict, errors.New("task is issuing"))
		return
	}
	m := h.getCertManager()
	store := h.secrets
	if err := cert.IssueNow(c.Request.Context(), h.db, m, store, row.ID, nil); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// getCertManager builds the orchestration executor (the environment conventions match the config).
func (h *Handler) getCertManager() *cert.Manager {
	return &cert.Manager{
		Bin: h.cfg.AcmeBin, Home: h.cfg.AcmeHome,
		Webroot: h.cfg.AcmeWebroot, CA: "letsencrypt",
	}
}
