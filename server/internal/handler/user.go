package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handler) listUsers(c *gin.Context) {
	out := []storage.User{}
	if err := h.db.Order("id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) getUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	c.JSON(http.StatusOK, u)
}

func (h *Handler) createUser(c *gin.Context) {
	var in model.UserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || len(username) > 64 {
		fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
		return
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		fail(c, http.StatusBadRequest, errors.New("quota_bytes must be >= 0 (0 = unlimited)"))
		return
	}
	if err := h.ensureUsernameFree(username, 0); err != nil {
		if errors.Is(err, errUsernameTaken) {
			fail(c, http.StatusConflict, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	u := storage.User{
		Username:   username,
		SubToken:   token,
		QuotaBytes: quotaOrDefault(in.QuotaBytes),
		ExpiresAt:  in.ExpiresAt,
		Enabled:    enabled,
	}
	if err := h.db.Create(&u).Error; err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("username already exists"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, u)
}

func (h *Handler) updateUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in model.UserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if strings.TrimSpace(in.Username) != "" {
		// Username 非空即视为改名（PATCH 语义，空串不改）。
		username := strings.TrimSpace(in.Username)
		if len(username) > 64 {
			fail(c, http.StatusBadRequest, errors.New("username is required (1-64 chars)"))
			return
		}
		if err := h.ensureUsernameFree(username, u.ID); err != nil {
			if errors.Is(err, errUsernameTaken) {
				fail(c, http.StatusConflict, err)
				return
			}
			fail(c, http.StatusInternalServerError, err)
			return
		}
		updates["username"] = username
	}
	if in.QuotaBytes != nil {
		if *in.QuotaBytes < 0 {
			fail(c, http.StatusBadRequest, errors.New("quota_bytes must be >= 0 (0 = unlimited)"))
			return
		}
		updates["quota_bytes"] = *in.QuotaBytes
	}
	if in.ExpiresAt != nil {
		updates["expires_at"] = *in.ExpiresAt
	}
	if in.ClearExpires != nil && *in.ClearExpires {
		updates["expires_at"] = nil
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if len(updates) > 0 {
		if err := h.db.Model(&storage.User{}).Where("id=?", u.ID).Updates(updates).Error; err != nil {
			if isDup(err) {
				fail(c, http.StatusConflict, errors.New("username already exists"))
				return
			}
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	fresh, err := h.findUser(strconv.FormatUint(uint64(u.ID), 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, fresh)
}

func (h *Handler) deleteUser(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	if err := h.db.Delete(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": u.ID})
}

// resetSubToken 重置订阅令牌：旧链接立即失效（对齐安全设计 §4）。
func (h *Handler) resetSubToken(c *gin.Context) {
	u, err := h.findUser(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&storage.User{}).Where("id=?", u.ID).Update("sub_token", token).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	u.SubToken = token
	c.JSON(http.StatusOK, u)
}

func (h *Handler) findUser(id string) (storage.User, error) {
	var u storage.User
	uid, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return u, errNotFound
	}
	if err := h.db.First(&u, uid).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return u, errNotFound
		}
		return u, err
	}
	return u, nil
}

// errUsernameTaken 表示用户名重复。
var errUsernameTaken = errors.New("username already exists")

// ensureUsernameFree 检查用户名未被其他用户占用。
func (h *Handler) ensureUsernameFree(username string, selfID uint) error {
	var cnt int64
	q := h.db.Model(&storage.User{}).Where("username=?", username)
	if selfID > 0 {
		q = q.Where("id<>?", selfID)
	}
	if err := q.Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return errUsernameTaken
	}
	return nil
}

func quotaOrDefault(p *int64) int64 {
	if p == nil {
		return 0 // 0 = 不限
	}
	return *p
}

func isDup(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}
