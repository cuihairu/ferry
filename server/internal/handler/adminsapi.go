package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Multi-tier admin / sub-admin quota (P2-2) management surface: the two-tier
// policy aligns with Marzban's is_sudo -- super (the bootstrap account) can manage
// admins, while operator (sub-admin) is an IsAdmin user bound by the max_users
// sign-up quota (see createUser in user.go). An admin is an IsAdmin row in the
// users table, so these endpoints promote/demote and maintain quotas rather than
// using a separate table.

// requireSuper is the admin tier gate: anyone who is not super (including operator,
// and legacy tokens that still normalize to non-super) gets 403. Attached to the
// /api/admins group.
func (h *Handler) requireSuper() gin.HandlerFunc {
	return func(c *gin.Context) {
		if role, _ := c.Get("adminRole"); role != RoleSuper {
			c.JSON(http.StatusForbidden, gin.H{"error": "需要超级管理员权限"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// adminRow is the wire shape for admin list/detail responses (never leaks
// password/TOTP/subscription token).
type adminRow struct {
	ID        uint   `json:"id"`
	Username  string `json:"username"`
	Enabled   bool   `json:"enabled"`
	Role      string `json:"role"`
	MaxUsers  int    `json:"max_users"`
	TOTPOn    bool   `json:"totp_enabled"`
	CreatedBy string `json:"created_by"`
}

func toAdminRow(u storage.User) adminRow {
	return adminRow{
		ID: u.ID, Username: u.Username, Enabled: u.Enabled,
		Role: normalizeAdminRole(u.AdminRole), MaxUsers: u.MaxUsers,
		TOTPOn: u.TOTPEnabled, CreatedBy: u.CreatedBy,
	}
}

// checkOperatorQuota is the sub-admin sign-up quota (P2-2): counts are charged by
// created_by, and max_users=0 means unlimited; the over-quota error is turned
// into 403 by createUser.
func (h *Handler) checkOperatorQuota(operator string) error {
	var op storage.User
	if err := h.db.Where("username = ? AND is_admin = ?", operator, true).First(&op).Error; err != nil {
		return errors.New("operator account not found")
	}
	if op.MaxUsers <= 0 {
		return nil
	}
	var used int64
	if err := h.db.Model(&storage.User{}).Where("created_by = ?", operator).Count(&used).Error; err != nil {
		return err
	}
	if used >= int64(op.MaxUsers) {
		return errors.New("operator user quota exhausted")
	}
	return nil
}

// listAdmins lists all admins (GET /api/admins, super).
func (h *Handler) listAdmins(c *gin.Context) {
	rows := []storage.User{}
	if err := h.db.Where("is_admin = ?", true).Order("id").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]adminRow, 0, len(rows))
	for _, u := range rows {
		out = append(out, toAdminRow(u))
	}
	c.JSON(http.StatusOK, out)
}

// createAdmin promotes a sub-admin (POST /api/admins, super): creates an IsAdmin
// user whose role is always operator (super only ever comes from bootstrap, so the
// API cannot mint a super admin); the password goes to users.password as a bcrypt
// hash; max_users >= 0 (0 = unlimited).
func (h *Handler) createAdmin(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		MaxUsers *int   `json:"max_users"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
		return
	}
	if len(in.Password) < 8 {
		fail(c, http.StatusBadRequest, errors.New("password must be at least 8 chars"))
		return
	}
	maxUsers := 0
	if in.MaxUsers != nil {
		if *in.MaxUsers < 0 {
			fail(c, http.StatusBadRequest, errors.New("max_users must be >= 0 (0 = unlimited)"))
			return
		}
		maxUsers = *in.MaxUsers
	}
	if err := h.ensureUsernameFree(username, 0); err != nil {
		if errors.Is(err, errUsernameTaken) {
			fail(c, http.StatusConflict, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	u := storage.User{
		Username: username, SubToken: token, Password: string(hash),
		Enabled: true, IsAdmin: true, AdminRole: RoleOperator, MaxUsers: maxUsers,
	}
	if err := h.db.Create(&u).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, toAdminRow(u))
}

// updateAdmin maintains a sub-admin (PUT /api/admins/:id, super): change password /
// enable-disable / quota. The super account itself cannot be modified through this
// endpoint (self-lockout protection -- enable/disable and quota lowering only apply
// to operator).
func (h *Handler) updateAdmin(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var u storage.User
	if err := h.db.First(&u, id).Error; err != nil {
		// gorm's ErrRecordNotFound is not the same sentinel as errNotFound, so it must be
		// checked explicitly; otherwise replyFind turns it into a 500 (the user-facing
		// answer should be 404).
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("admin not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !u.IsAdmin {
		fail(c, http.StatusNotFound, errors.New("not an admin"))
		return
	}
	if normalizeAdminRole(u.AdminRole) == RoleSuper {
		fail(c, http.StatusForbidden, errors.New("super admin is not editable here"))
		return
	}
	var in struct {
		Password *string `json:"password"`
		Enabled  *bool   `json:"enabled"`
		MaxUsers *int    `json:"max_users"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if in.Password != nil {
		if len(*in.Password) < 8 {
			fail(c, http.StatusBadRequest, errors.New("password must be at least 8 chars"))
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		updates["password"] = string(hash)
		// A password change invalidates old admin JWTs immediately (audit fix 2026-11-12):
		// the middleware re-check compares token issue time against this change time,
		// and any token older than the change is 401.
		now := time.Now()
		updates["password_changed_at"] = now
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if in.MaxUsers != nil {
		if *in.MaxUsers < 0 {
			fail(c, http.StatusBadRequest, errors.New("max_users must be >= 0 (0 = unlimited)"))
			return
		}
		updates["max_users"] = *in.MaxUsers
	}
	if len(updates) == 0 {
		fail(c, http.StatusBadRequest, errors.New("nothing to update"))
		return
	}
	if err := h.db.Model(&storage.User{}).Where("id = ?", u.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var out storage.User
	h.db.First(&out, u.ID)
	c.JSON(http.StatusOK, toAdminRow(out))
}

// deleteAdmin demotes a sub-admin (DELETE /api/admins/:id, super): it does not delete
// the user (who may have orders/quota data under their name) but strips the admin
// identity -- is_admin=false, role cleared, password cleared -- dropping them back
// to a regular subscriber. A super account cannot be demoted (self-lockout
// protection for the sole super admin).
func (h *Handler) deleteAdmin(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var u storage.User
	if err := h.db.First(&u, id).Error; err != nil {
		// Same as update for the not-found case: explicitly detect a missing gorm record and return 404; replyFind would turn it into 500.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("admin not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !u.IsAdmin {
		fail(c, http.StatusNotFound, errors.New("not an admin"))
		return
	}
	if normalizeAdminRole(u.AdminRole) == RoleSuper {
		fail(c, http.StatusForbidden, errors.New("super admin cannot be demoted"))
		return
	}
	if err := h.db.Model(&storage.User{}).Where("id = ?", u.ID).Updates(map[string]any{
		"is_admin": false, "admin_role": "", "password": "",
	}).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"demoted": true})
}
