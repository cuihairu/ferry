package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// 多级管理员/子管理员配额（P2-2）管理面：两级口径对齐 Marzban is_sudo——
// super（bootstrap 建号）可管管理员；operator（子管理员）为 IsAdmin 用户，
// 受 max_users 建用户配额约束（见 user.go createUser）。管理员=users 表
// IsAdmin 行，本组端点做的是提升/降级与配额维护，不另设表。

// requireSuper 管理员层级门槛：非 super（含 operator 与旧令牌归一后仍非
// super 的）一律 403。挂在 /api/admins 组上。
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

// adminRow 是管理员列表/详情的下发形态（不泄密码/TOTP/订阅令牌）。
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

// checkOperatorQuota 子管理员建用户配额（P2-2）：按 created_by 归账计数，
// max_users=0 不限；超配额回错由 createUser 转 403。
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

// listAdmins 列出全部管理员（GET /api/admins，super）。
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

// createAdmin 提升子管理员（POST /api/admins，super）：建 IsAdmin 用户，
// role 恒 operator（super 只由 bootstrap 产生，杜绝 API 造超管）；密码
// bcrypt 落 users.password；max_users>=0（0=不限）。
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

// updateAdmin 维护子管理员（PUT /api/admins/:id，super）：改密码/启停/
// 配额。super 账号本体不可经此端点改动（防自锁——启停与降配额只对
// operator 生效）。
func (h *Handler) updateAdmin(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var u storage.User
	if err := h.db.First(&u, id).Error; err != nil {
		replyFind(c, err)
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

// deleteAdmin 降级子管理员（DELETE /api/admins/:id，super）：不是删用户
// （名下可能有订单/配额数据），而是摘管理员身份——is_admin=false、角色清
// 空、密码清空，回落普通订阅用户。super 账号不可降（唯一超管防自锁）。
func (h *Handler) deleteAdmin(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var u storage.User
	if err := h.db.First(&u, id).Error; err != nil {
		replyFind(c, err)
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
