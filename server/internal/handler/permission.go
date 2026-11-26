package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

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
)

// adminPerms is the grantable vocabulary keyed by perm name.
var adminPerms = map[string]string{
	PermCloudAccount: "cloud account management (providers, credentials, sync)",
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
// the grant table; no identity or no row both deny.
func (h *Handler) adminHasPerm(c *gin.Context, perm string) bool {
	idStr, _ := c.Get("adminUserID")
	s, ok := idStr.(string)
	if !ok || s == "" {
		return false
	}
	uid, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return false
	}
	var n int64
	h.db.Model(&storage.AdminPerm{}).Where("user_id = ? AND perm = ?", uid, perm).Count(&n)
	return n > 0
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
	var in struct {
		Perms []string `json:"perms"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	for _, p := range in.Perms {
		if _, ok := adminPerms[p]; !ok {
			fail(c, http.StatusBadRequest, fmt.Errorf("unknown permission %s", p))
			return
		}
	}
	actor, _ := h.auditActor(c)
	err = h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", id).Delete(&storage.AdminPerm{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, p := range in.Perms {
			row := storage.AdminPerm{UserID: uint(id), Perm: p, GrantedAt: now, GrantedBy: actor}
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
