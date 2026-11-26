package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Admin side of provisioning execution (OS-2): controlled triggering of template
// plan/apply and querying the job trail. Execution runs in a background
// goroutine (one tofu round can take minutes) and the API returns the running
// job immediately; keys are decrypted only on the server side and passed into
// the execution environment, never in API responses or logs.

func (h *Handler) getProvisionManager() *provision.Manager {
	// Shared: the same instance as the recovery/backfill loops (wired in
	// main.go); only serialized Execute covers "manual trigger x background
	// pipeline" crossings.
	return provision.Shared(h.db, h.cfg.TofuBin, h.cfg.TofuWorkdir, nil)
}

type provisionRunInput struct {
	Name string `json:"name"` // instance name (label); falls back to the template name when empty
}

// runProvision is the shared entry point for plan/apply: template and credential
// validation -> background execution -> running job.
func (h *Handler) runProvision(c *gin.Context, action string) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var tpl storage.ProvisionTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	var prov storage.Provider
	if err := h.db.First(&prov, tpl.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("template provider not found"))
		return
	}
	if !prov.Enabled {
		fail(c, http.StatusBadRequest, errors.New("provider disabled"))
		return
	}
	if !h.secrets.Enabled() {
		fail(c, http.StatusBadRequest, errors.New("secret master key not configured (set FERRY_SECRET_KEY)"))
		return
	}
	apiKey, err := provision.DecryptKey(prov, h.secrets)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var in provisionRunInput
	_ = c.ShouldBindJSON(&in) // name is optional
	name := in.Name
	if name == "" {
		name = tpl.Name
	}

	m := h.getProvisionManager()
	var job storage.ProvisionJob
	if action == "apply" {
		// OS-3: apply pre-mints the node and renders cloud-init (the token is
		// written into agent.json ahead of time, so the first hello right after
		// the agent is installed at boot registers it); preparation is
		// synchronous and takes seconds, execution goes to the background. The
		// same PrepareLaunch path is shared with BR-1 recovery provisioning.
		prep, err := m.PrepareLaunch(provision.LaunchParams{
			Template: tpl, Provider: prov, Store: h.secrets,
			InstanceName: name,
			PanelWSURL:   provision.PanelWSURL(h.cfg.BaseURL),
			AgentBase:    h.cfg.AgentDownloadBase, AgentVersion: h.cfg.AgentVersion,
			NewToken: RandomToken,
		})
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		job = *prep.Job
		go func(p *provision.Prepared) {
			status, log := m.Execute(context.Background(), p.Params)
			m.FinishJob(p.Job.ID, status, log)
		}(prep)
	} else {
		// plan: touches neither instances nor nodes.
		job = storage.ProvisionJob{TemplateID: tpl.ID, TemplateName: tpl.Name, Action: "plan", Status: "running"}
		if err := h.db.Create(&job).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		go func(jobID uint) {
			status, log := m.Execute(context.Background(), provision.ExecParams{
				Template: tpl, ProviderType: prov.Type, APIKey: apiKey,
				InstanceName: name, Action: "plan",
			})
			m.FinishJob(jobID, status, log)
		}(job.ID)
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) applyTemplate(c *gin.Context) { h.runProvision(c, "apply") }
func (h *Handler) planTemplate(c *gin.Context)  { h.runProvision(c, "plan") }

// listProvisionJobs returns the provisioning execution trail (newest first, 50
// rows by default).
func (h *Handler) listProvisionJobs(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	out := []storage.ProvisionJob{}
	if err := h.db.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listRecoveries returns ban-recovery pipeline states (BR-1, newest first, 50
// rows by default).
func (h *Handler) listRecoveries(c *gin.Context) {
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	out := []storage.Recovery{}
	if err := h.db.Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ackRecovery resolves a human-intervention pipeline (F-1645): manual -> failed
// so the row closes and the node rides the normal cooldown/reopen path; only a
// manual row can be acked.
func (h *Handler) ackRecovery(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var rec storage.Recovery
	if err := h.db.First(&rec, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if rec.State != recovery.StateManual {
		fail(c, http.StatusConflict, fmt.Errorf("recovery is %s; only manual pipelines can be acked", rec.State))
		return
	}
	now := time.Now()
	// The ack doubles as a fresh re-entry budget (F-1646): a human reviewed the
	// node, so the capped counter starts over from here.
	if err := h.db.Model(&storage.Recovery{}).Where("id = ? AND state = ?", rec.ID, recovery.StateManual).
		Updates(map[string]any{"state": recovery.StateFailed, "reopens": 0, "updated_at": now}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rec.State = recovery.StateFailed
	c.JSON(http.StatusOK, rec)
}

// listRecoveryActions returns one pipeline's action trail (BR-5 replay, levels
// ascending).
func (h *Handler) listRecoveryActions(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	out := []storage.RecoveryAction{}
	if err := h.db.Where("recovery_id = ?", id).Order("id ASC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
