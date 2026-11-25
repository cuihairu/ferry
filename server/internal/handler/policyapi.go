package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/policy"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

type policyInput struct {
	Scope       string          `json:"scope"`
	SubjectID   uint            `json:"subject_id"`
	Constraints json.RawMessage `json:"constraints"`
	Enabled     *bool           `json:"enabled,omitempty"`
}

// validatePolicyInput checks the scope and decodes the constraint payload; the
// subject must exist for plan/user scopes (system ignores the subject).
func (h *Handler) validatePolicyInput(in *policyInput, current storage.Policy) (string, storage.Policy, error) {
	scope := in.Scope
	if scope == "" {
		scope = current.Scope
	}
	if !storage.ValidPolicyScope(scope) {
		return "", storage.Policy{}, errors.New("scope must be system/plan/user")
	}
	subject := in.SubjectID
	if in.SubjectID == 0 && current.ID != 0 && current.Scope == scope {
		subject = current.SubjectID // update path: omitted keeps the current subject
	}
	if scope == storage.PolicyScopeSystem {
		subject = 0
	} else if subject == 0 {
		return "", storage.Policy{}, errors.New("subject_id is required for plan/user scope")
	} else if scope == storage.PolicyScopePlan {
		var p storage.Plan
		if err := h.db.First(&p, subject).Error; err != nil {
			return "", storage.Policy{}, errors.New("plan not found")
		}
	} else {
		var u storage.User
		if err := h.db.First(&u, subject).Error; err != nil {
			return "", storage.Policy{}, errors.New("user not found")
		}
	}
	raw := "{}"
	if len(in.Constraints) > 0 {
		raw = string(in.Constraints)
	} else if current.ID != 0 {
		raw = current.Constraints
	}
	if _, err := storage.ParsePolicyConstraints(raw); err != nil {
		return "", storage.Policy{}, err
	}
	// Group pin target (F-0270): the path group must exist when referenced.
	pc, _ := storage.ParsePolicyConstraints(raw)
	if pc.PathGroupID != 0 {
		var g storage.PathGroup
		if err := h.db.First(&g, pc.PathGroupID).Error; err != nil {
			return "", storage.Policy{}, errors.New("path group not found")
		}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	} else if current.ID != 0 {
		enabled = current.Enabled
	}
	return scope, storage.Policy{
		Scope: scope, SubjectID: subject,
		Constraints: raw, Enabled: enabled,
	}, nil
}

func (h *Handler) listPolicies(c *gin.Context) {
	out := []storage.Policy{}
	if err := h.db.Order("scope, subject_id, id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) createPolicy(c *gin.Context) {
	var in policyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	_, p, err := h.validatePolicyInput(&in, storage.Policy{})
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	p.Version = 1
	actor, _ := h.auditActor(c)
	p.UpdatedBy = actor
	// One policy per scope+subject: a duplicate target is a conflict, not a second row.
	var existing int64
	h.db.Model(&storage.Policy{}).Where("scope = ? AND subject_id = ?", p.Scope, p.SubjectID).Count(&existing)
	if existing > 0 {
		fail(c, http.StatusConflict, errors.New("policy already exists for this scope and subject"))
		return
	}
	if err := h.db.Create(&p).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *Handler) updatePolicy(c *gin.Context) {
	var cur storage.Policy
	if err := h.db.First(&cur, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("policy not found"))
		return
	}
	var in policyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	_, p, err := h.validatePolicyInput(&in, cur)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{
		"scope": p.Scope, "subject_id": p.SubjectID,
		"constraints": p.Constraints, "enabled": p.Enabled,
		"version": cur.Version + 1, "updated_by": actorFrom(c, h),
	}
	if err := h.db.Model(&storage.Policy{}).Where("id = ?", cur.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var updated storage.Policy
	if err := h.db.First(&updated, cur.ID).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

// actorFrom pulls the audit actor name for map-style updates.
func actorFrom(c *gin.Context, h *Handler) string {
	actor, _ := h.auditActor(c)
	return actor
}

func (h *Handler) deletePolicy(c *gin.Context) {
	res := h.db.Where("id = ?", c.Param("id")).Delete(&storage.Policy{})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("policy not found"))
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// resolvePolicyView returns the effective constraints for a subject chain
// (F-0287 made visible): user_id/plan_id query params, 0 = none.
func (h *Handler) resolvePolicyView(c *gin.Context) {
	userID := queryUint(c, "user_id")
	planID := queryUint(c, "plan_id")
	if u := c.Query("user_id"); u != "" && userID > 0 {
		var user storage.User
		if err := h.db.First(&user, userID).Error; err != nil {
			fail(c, http.StatusNotFound, errors.New("user not found"))
			return
		}
	}
	eff, rows, err := policy.Resolve(h.db, userID, planID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"constraints": eff,
		"policies":    rows,
	})
}

// simulatePolicy is the explain interface (F-0296): it evaluates the resolved
// constraints for the subject chain against the current entry pool and
// returns who stays, who leaves and why.
func (h *Handler) simulatePolicy(c *gin.Context) {
	userID := queryUint(c, "user_id")
	planID := queryUint(c, "plan_id")
	eff, rows, err := policy.Resolve(h.db, userID, planID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	// Same candidate shape as subscription delivery: entry/both nodes in the
	// active pool (enabled, not in maintenance, not provisioning).
	var nodes []storage.Node
	if err := h.db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
		true, false, "entry", "both", "active", "provisioning").
		Order("id").Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	kept, dropped := policy.ApplyConstraints(nodes, eff)
	// Group pin resolution (F-0270): the explain view shows which member the
	// group would serve right now.
	var group *pathhealth.ResolvedPath
	if eff.PathGroupID != 0 {
		g, err := pathhealth.ResolveGroupPath(h.db, eff.PathGroupID, time.Now())
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		group = g
	}
	c.JSON(http.StatusOK, gin.H{
		"constraints":   eff,
		"policies":      rows,
		"kept":          kept,
		"dropped":       dropped,
		"failover_free": policy.FreezeFailover(eff),
		"path_group":    group,
	})
}

// queryUint parses a non-negative int query param; a bad value reads as 0.
func queryUint(c *gin.Context, name string) uint {
	v, err := strconv.ParseUint(strings.TrimSpace(c.Query(name)), 10, 64)
	if err != nil {
		return 0
	}
	return uint(v)
}
