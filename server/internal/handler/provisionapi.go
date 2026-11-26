package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/cost"
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
	// F-1654: spec validation before any execution — an apply/plan that cannot
	// render or place is refused here, never sent to tofu.
	if errs, _ := h.specCheck(c.Request.Context(), tpl, prov, apiKey); len(errs) > 0 {
		fail(c, http.StatusBadRequest, errors.New("template spec invalid: "+strings.Join(errs, "; ")))
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
		// F-1652: execution rides the shared task ledger (tracked row, cancel,
		// stage/log); a ledger failure falls back to the direct goroutine so a
		// minted node is never stranded unexecuted.
		ran := false
		if h.tasks != nil {
			if _, err := h.tasks.Enqueue("cloud.create", job.ID,
				fmt.Sprintf("provision-job-%d", job.ID), h.taskActor(c)); err == nil {
				ran = true
			}
		}
		if !ran {
			go func(p *provision.Prepared) {
				status, log := applyWithCleanup(context.Background(), m, h.db,
					h.cfg.ProvisionCleanupOnFail, p.Params, p.Job.ID)
				m.FinishJob(p.Job.ID, status, log)
			}(prep)
		}
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

// specCheck validates a template's placement spec before any execution
// (F-1654): the local minimums a tofu run cannot survive without (plan, region,
// a renderable provider type) plus, when a cloudprov adapter is registered for
// the type, the region against the account-visible list. Vendor API failures
// degrade to warnings — apply is not blocked on a flaky vendor control plane,
// the tofu run surfaces vendor errors itself.
func (h *Handler) specCheck(ctx context.Context, tpl storage.ProvisionTemplate, prov storage.Provider, apiKey string) (errs, warns []string) {
	if strings.TrimSpace(tpl.Plan) == "" {
		errs = append(errs, "plan is required")
	}
	if strings.TrimSpace(tpl.Region) == "" {
		errs = append(errs, "region is required")
	}
	// RenderHCL is the renderability oracle: an unsupported provider type can
	// never produce HCL, so the template is dead weight until the type lands.
	if _, err := provision.RenderHCL(prov.Type, tpl, "spec-check", ""); err != nil {
		errs = append(errs, err.Error())
	}
	factory := cloudprov.Lookup(prov.Type)
	if factory == nil {
		return errs, warns
	}
	a, err := factory(cloudprov.Account{
		ProviderID: prov.ID, Name: prov.Name, Type: prov.Type,
		AccessKey: apiKey, Region: prov.Region,
	})
	if err != nil {
		warns = append(warns, "adapter build failed: "+err.Error())
		return errs, warns
	}
	caps, err := cloudprov.NewClient(a, cloudprov.Defaults).Capabilities(ctx)
	if err != nil {
		warns = append(warns, "region check unavailable (vendor API): "+err.Error())
		return errs, warns
	}
	if len(caps.Regions) > 0 && !slices.Contains(caps.Regions, tpl.Region) {
		errs = append(errs, fmt.Sprintf("region %q not in provider %s account regions %v",
			tpl.Region, prov.Name, caps.Regions))
	}
	return errs, warns
}

// validateSpec serves POST /api/provision-templates/:id/validate (F-1654): a
// dry run of the apply chain's checks — template/provider/master key/credential
// plus specCheck — without touching instances. Always 200 with ok/errors/
// warnings so the panel can render the report; only an unknown template is 404.
func (h *Handler) validateSpec(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	var tpl storage.ProvisionTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	var errs, warns []string
	var prov storage.Provider
	if err := h.db.First(&prov, tpl.ProviderID).Error; err != nil {
		errs = append(errs, "template provider not found")
	} else {
		if !prov.Enabled {
			errs = append(errs, "provider disabled")
		}
		if !h.secrets.Enabled() {
			errs = append(errs, "secret master key not configured (set FERRY_SECRET_KEY)")
		} else if apiKey, derr := provision.DecryptKey(prov, h.secrets); derr != nil {
			errs = append(errs, "credential unreadable: "+derr.Error())
		} else {
			errs, warns = h.specCheck(c.Request.Context(), tpl, prov, apiKey)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": len(errs) == 0, "errors": errs, "warnings": warns})
}

// destroyBlockers lists what a template workspace destroy would break
// (F-1659): the workdir state is per-template, so destroy rolls back exactly
// this template's minted instances — and each of them that still lives as a
// node is checked against what depends on it: a path routing through it, a
// still-bound cloud resource, or an in-pool scheduling state.
func (h *Handler) destroyBlockers(tpl storage.ProvisionTemplate) []string {
	var names []string
	h.db.Model(&storage.ProvisionJob{}).Where("template_id = ? AND instance_name != ''", tpl.ID).
		Distinct().Pluck("instance_name", &names)
	var out []string
	for _, name := range names {
		var n storage.Node
		if err := h.db.Where("name = ?", name).First(&n).Error; err != nil {
			continue // minted but never registered: nothing in service to break
		}
		var paths int64
		h.db.Model(&storage.Path{}).Where(
			"entry_node_id = ? OR landing_node_id = ? OR relay_node_id = ?", n.ID, n.ID, n.ID).Count(&paths)
		if paths > 0 {
			out = append(out, fmt.Sprintf("node %s serves %d path(s)", n.Name, paths))
		}
		var bound int64
		h.db.Model(&storage.CloudResource{}).Where("node_id = ? AND unbound_at IS NULL", n.ID).Count(&bound)
		if bound > 0 {
			out = append(out, fmt.Sprintf("node %s has a bound cloud resource", n.Name))
		}
		if n.Enabled && n.PoolState == "active" {
			out = append(out, fmt.Sprintf("node %s is in the scheduling pool", n.Name))
		}
	}
	return out
}

// destroyCheck serves GET /api/provision-templates/:id/destroy-check (F-1659):
// the preflight for a destroy — ok=false plus the blocker list when anything
// in service depends on this template's instances.
func (h *Handler) destroyCheck(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	var tpl storage.ProvisionTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	blockers := h.destroyBlockers(tpl)
	c.JSON(http.StatusOK, gin.H{"ok": len(blockers) == 0, "blockers": blockers})
}

// templateCostEstimate serves GET /api/provision-templates/:id/cost-estimate
// (F-1653): the reference-price lookup mapped from the template, so the operator
// sees the expected monthly cost before paying for an apply. Keys default to
// provider name + template region + spec (bandwidth tier, falling back to the
// plan slug); query params override each segment, because the price table's spec
// vocabulary is source-managed and the template's plan slug may not match it.
// A miss is 200+hit=false, mirroring the node probe; the hand-entered
// MonthlyCostCents is compared only when ?currency= names its currency (the
// template carries no currency column).
func (h *Handler) templateCostEstimate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	var tpl storage.ProvisionTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("template not found"))
		return
	}
	src, err := h.refSource()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if src == nil {
		fail(c, http.StatusBadRequest, errors.New("reference price table not imported yet (PUT /api/cost/ref-table)"))
		return
	}
	var prov storage.Provider
	if err := h.db.First(&prov, tpl.ProviderID).Error; err != nil {
		fail(c, http.StatusBadRequest, errors.New("template provider not found"))
		return
	}

	q := costref.Query{Provider: prov.Name, Region: tpl.Region, Spec: cost.SpecOf(tpl.BwMbps, 0)}
	if q.Spec == "" {
		q.Spec = tpl.Plan
	}
	if v := c.Query("provider"); v != "" {
		q.Provider = v
	}
	if v := c.Query("region"); v != "" {
		q.Region = v
	}
	if v := c.Query("spec"); v != "" {
		q.Spec = v
	}

	out := gin.H{"source": src.Name(), "query": q,
		"manual_monthly_cents": tpl.MonthlyCostCents}
	quote, err := src.Lookup(c.Request.Context(), q)
	if errors.Is(err, costref.ErrNotFound) {
		out["hit"] = false
		c.JSON(http.StatusOK, out)
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out["hit"] = true
	out["quote"] = quote
	switch {
	case tpl.MonthlyCostCents <= 0:
		out["reason"] = "template monthly cost not set"
	case c.Query("currency") == "":
		out["reason"] = "template currency not set (pass ?currency= to compare)"
	default:
		dev, cerr := costref.Compare(costref.Manual{
			MonthlyCents: tpl.MonthlyCostCents, Currency: c.Query("currency")}, quote, refTolerance(c))
		switch {
		case cerr == nil:
			out["deviation"] = dev
		case errors.Is(cerr, costref.ErrCurrencyMismatch):
			out["reason"] = "currency mismatch (manual " + c.Query("currency") + " vs quote " + quote.Currency + ")"
		default:
			fail(c, http.StatusInternalServerError, cerr)
			return
		}
	}
	c.JSON(http.StatusOK, out)
}

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
