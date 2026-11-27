package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Named management permissions (F-1703~F-1708): the fine-grained tier below
// the super/operator roles. Super passes every check; every other admin needs
// an explicit AdminPerm row. The vocabulary is closed — a grant names a
// constant here or the grant is rejected, so a typo can never silently grant
// nothing.
const (
	// PermCloudAccount gates cloud account management (F-1703): provider
	// CRUD, credential rotate/revoke, connection test, account-wide sync.
	PermCloudAccount = "cloud.account.manage"
	// PermCloudPower gates the everyday power operations (F-1704): power on,
	// power off, reboot.
	PermCloudPower = "cloud.power"
	// PermCloudHighRisk gates force-stop and rebuild (F-1705): the data-loss
	// tier, stacked on top of the operation system's own confirmation flow.
	PermCloudHighRisk = "cloud.highrisk"
	// PermCostView gates the cost read surface (F-1706): aggregates, the bill
	// ledger, spend/anomaly views and the CSV export. Cost CHANGES are a
	// separate tier (F-1707).
	PermCostView = "cost.view"
	// PermCostManage gates the cost write surface (F-1707): thresholds, the
	// reference table, watch/allocation-rule/estimate/line/reconciliation
	// mutations and bill imports. Tiered apart from cost.view (an operator
	// usually carries both; neither implies the other).
	PermCostManage = "cost.manage"
	// PermNodeManage gates node operations (F-1708): node CRUD/approval,
	// enrollment tokens, health overrides, pool suspend/resume/standby,
	// config/probe-task/rulelib pushes, proc/upgrade (incl. batch), cloud
	// drain/boot-check and recovery acks. Node READS stay open.
	PermNodeManage = "node.manage"
	// PermPathManage gates path operations (F-1708): path CRUD, drafts and
	// auto-provision, path groups and members, landing registrations.
	PermPathManage = "path.manage"
)

// adminPerms is the grantable vocabulary keyed by perm name.
var adminPerms = map[string]string{
	PermCloudAccount:  "cloud account management (providers, credentials, sync)",
	PermCloudPower:    "cloud resource power operations (on/off/reboot)",
	PermCloudHighRisk: "high-risk cloud operations (force stop, rebuild)",
	PermCostView:      "cost view and bill export (read-only cost surface)",
	PermCostManage:    "cost changes and allocation rules (thresholds, imports, rules; pair with cost.view)",
	PermNodeManage:    "node operations (create/approve/pool/proc/config/upgrade)",
	PermPathManage:    "path operations (paths, drafts, path groups, landings)",
}

// scopable names the perms a grant may narrow to one resource (F-1710) and
// the scope kind each accepts: cloud tiers scope by provider (the cloud
// account), node ops by node. Everything else is global-only.
var scopable = map[string]string{
	PermCloudAccount:  "provider",
	PermCloudPower:    "provider",
	PermCloudHighRisk: "provider",
	PermNodeManage:    "node",
}

// validateScope checks a grant scope (F-1710): empty is global; otherwise the
// perm must be scopable, the scope must be "<kind>:<id>" in that perm's kind,
// and the target row must exist — a scope naming nothing would grant forever
// nothing (or everything, once the row appears).
func (h *Handler) validateScope(perm, scope string) error {
	if scope == "" {
		return nil
	}
	kind, ok := scopable[perm]
	if !ok {
		return fmt.Errorf("perm %s cannot carry a scope", perm)
	}
	if !strings.HasPrefix(scope, kind+":") {
		return fmt.Errorf("perm %s scopes by %s:<id>, got %s", perm, kind, scope)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(scope, kind+":"), 10, 64)
	if err != nil || id < 1 {
		return fmt.Errorf("scope %s: bad id", scope)
	}
	switch kind {
	case "provider":
		var n int64
		h.db.Model(&storage.Provider{}).Where("id = ?", id).Count(&n)
		if n == 0 {
			return fmt.Errorf("scope %s: provider not found", scope)
		}
	case "node":
		var n int64
		h.db.Model(&storage.Node{}).Where("id = ?", id).Count(&n)
		if n == 0 {
			return fmt.Errorf("scope %s: node not found", scope)
		}
	}
	return nil
}

// hasPermRow reports whether the admin holds a matching grant: a global row
// (scope "") passes every check for the perm; a scoped row passes only when
// its scope equals wantScope. wantScope "" is the resource-blind form — only
// a global row qualifies.
func (h *Handler) hasPermRow(c *gin.Context, perm, wantScope string) bool {
	idStr, _ := c.Get("adminUserID")
	s, ok := idStr.(string)
	if !ok || s == "" {
		return false
	}
	uid, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return false
	}
	var rows []storage.AdminPerm
	h.db.Where("user_id = ? AND perm = ?", uid, perm).Find(&rows)
	for _, r := range rows {
		// A global grant passes everywhere; a scoped grant only on its resource.
		if r.Scope == "" || r.Scope == wantScope {
			return true
		}
	}
	return false
}

// requirePermOn gates a resource-addressed route behind a named permission
// (F-1710): super passes, a global grant passes, and a scoped grant passes
// only when its scope names exactly this route's resource ("<kind>:<id>"
// from the :id path param).
func (h *Handler) requirePermOn(perm, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get("adminRole"); role == RoleSuper {
			c.Next()
			return
		}
		want := kind + ":" + c.Param("id")
		if !h.hasPermRow(c, perm, want) {
			fail(c, http.StatusForbidden, fmt.Errorf("missing permission %s on %s", perm, want))
			c.Abort()
			return
		}
		c.Next()
	}
}

// opPerm maps each cloud operation type to its gate (F-1704/F-1705); ops
// outside the map are not permission-gated (unknown ops fail validOp later).
var opPerm = map[string]string{
	cloudprov.OpPowerOn:   PermCloudPower,
	cloudprov.OpPowerOff:  PermCloudPower,
	cloudprov.OpReboot:    PermCloudPower,
	cloudprov.OpForceStop: PermCloudHighRisk,
	cloudprov.OpRebuild:   PermCloudHighRisk,
}

// opPermCheck is the in-handler gate for POST /api/cloud-operations (the
// middleware form cannot see the body): super passes unconditionally, every
// other admin needs the op-type grant — global, or scoped to the request's
// cloud account (F-1710).
func (h *Handler) opPermCheck(c *gin.Context, op string, providerID uint) bool {
	if role, _ := c.Get("adminRole"); role == RoleSuper {
		return true
	}
	perm, ok := opPerm[op]
	if !ok {
		return true
	}
	return h.hasPermRow(c, perm, fmt.Sprintf("provider:%d", providerID))
}

// requirePerm gates one route behind a named permission: super admins pass
// unconditionally, everyone else must carry the grant row. Requests without
// an admin identity (plain API tokens) fail the check closed.
func (h *Handler) requirePerm(perm string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get("adminRole"); role == RoleSuper {
			c.Next()
			return
		}
		if !h.adminHasPerm(c, perm) {
			// Abort, not just fail: a bare fail writes the 403 but leaves the
			// chain running, so the guarded handler would still execute.
			fail(c, http.StatusForbidden, fmt.Errorf("missing permission %s", perm))
			c.Abort()
			return
		}
		c.Next()
	}
}

// adminHasPerm resolves the admin identity from the auth context and checks
// the grant table for a GLOBAL row: resource-blind routes cannot address a
// scope, so only scope "" qualifies (F-1710). No identity or no row deny.
func (h *Handler) adminHasPerm(c *gin.Context, perm string) bool {
	return h.hasPermRow(c, perm, "")
}

// listAdminPerms serves GET /api/admins/:id/perms (super only): the target's
// grant rows.
func (h *Handler) listAdminPerms(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		fail(c, http.StatusNotFound, fmt.Errorf("admin not found"))
		return
	}
	var u storage.User
	if err := h.db.Where("is_admin = ?", true).First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, fmt.Errorf("admin not found"))
		return
	}
	out := []storage.AdminPerm{}
	if err := h.db.Where("user_id = ?", id).Order("perm ASC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// putAdminPerms serves PUT /api/admins/:id/perms (super only): replaces the
// target's grant set wholesale. Unknown perm names reject the whole write so
// a typo'd grant can never land half-applied.
func (h *Handler) putAdminPerms(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		fail(c, http.StatusNotFound, fmt.Errorf("admin not found"))
		return
	}
	var u storage.User
	if err := h.db.Where("is_admin = ?", true).First(&u, id).Error; err != nil {
		fail(c, http.StatusNotFound, fmt.Errorf("admin not found"))
		return
	}
	// F-1710: each entry is either a plain perm name (global) or an object
	// {perm, scope}; unknown names and invalid scopes reject the whole write
	// so a typo'd grant can never land half-applied.
	var in struct {
		Perms []json.RawMessage `json:"perms"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	type grant struct {
		Perm  string `json:"perm"`
		Scope string `json:"scope"`
	}
	grants := make([]grant, 0, len(in.Perms))
	for _, raw := range in.Perms {
		var name string
		g := grant{}
		if err := json.Unmarshal(raw, &name); err == nil {
			g.Perm = name
		} else {
			if err := json.Unmarshal(raw, &g); err != nil || g.Perm == "" {
				fail(c, http.StatusBadRequest, fmt.Errorf("perm entry must be a name or {perm, scope}"))
				return
			}
		}
		if _, ok := adminPerms[g.Perm]; !ok {
			fail(c, http.StatusBadRequest, fmt.Errorf("unknown permission %s", g.Perm))
			return
		}
		if err := h.validateScope(g.Perm, g.Scope); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		grants = append(grants, g)
	}
	actor, _ := h.auditActor(c)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&storage.AdminPerm{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, g := range grants {
			row := storage.AdminPerm{UserID: uint(id), Perm: g.Perm, Scope: g.Scope, GrantedAt: now, GrantedBy: actor}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := []storage.AdminPerm{}
	h.db.Where("user_id = ?", id).Order("perm ASC").Find(&out)
	c.JSON(http.StatusOK, out)
}

// readConfirm pulls the confirm field out of a request body (F-1709); an
// absent or unparseable body is an empty confirm, which never matches.
func readConfirm(c *gin.Context) string {
	var in struct {
		Confirm string `json:"confirm"`
	}
	_ = c.ShouldBindJSON(&in)
	return in.Confirm
}

// confirmGate is the server-side second confirmation (F-1709): a destructive
// request must echo the designated string in its confirm body field, so a
// stray click or scripted call cannot land alone (same shape as the F-1660
// template destroy gate).
func confirmGate(c *gin.Context, want, verb string) bool {
	if got := readConfirm(c); got != want {
		fail(c, http.StatusBadRequest, fmt.Errorf("confirmation mismatch: pass confirm=%s to %s", want, verb))
		return false
	}
	return true
}
