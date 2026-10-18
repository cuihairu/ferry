package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 一次性节点注册凭据（FERRY-001）：管理面 /api/nodes 受 apiAuth 保护，安装
// 脚本首装不该持管理员凭据，也不能开匿名建节点。流程：管理员在 dash/API 侧
// 签发短时一次性凭据（POST /api/enrollment-tokens，走 apiAuth+审计）→
// 节点机 --enroll-token 携带它调公开面 POST /api/enroll 换节点令牌 →
// 凭据用过即废，泄露影响面=可注册一个节点（可吊销可追责：行上有签发人与
// 消费痕迹，公开面不经 auditMiddleware，本行即审计载体）。

const (
	enrollTTLDefaultMinutes = 60
	enrollTTLMaxMinutes     = 1440
)

// errEnrollTokenInvalid 覆盖不存在/过期/已用/已吊销四种态：统一 401 文案，
// 不给探测方区分凭据状态的口子。
var errEnrollTokenInvalid = errors.New("注册凭据无效或已过期")

// createEnrollmentToken 签发一次性注册凭据（POST /api/enrollment-tokens，
// 管理数据面）：token 明文只在本次响应出现，ttl 缺省 60 分钟、上限一天。
func (h *Handler) createEnrollmentToken(c *gin.Context) {
	var in model.EnrollmentTokenInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	ttl := enrollTTLDefaultMinutes
	if in.TTLMinutes != nil {
		if *in.TTLMinutes < 1 || *in.TTLMinutes > enrollTTLMaxMinutes {
			fail(c, http.StatusBadRequest, errors.New("ttl_minutes must be 1-1440"))
			return
		}
		ttl = *in.TTLMinutes
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	e := storage.EnrollmentToken{
		Token:     token,
		Note:      in.Note,
		CreatedBy: actorOf(c),
		ExpiresAt: time.Now().Add(time.Duration(ttl) * time.Minute),
	}
	if err := h.db.Create(&e).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id": e.ID, "token": e.Token, "note": e.Note,
		"expires_at": e.ExpiresAt, "ttl_minutes": ttl,
	})
}

// actorOf 取当前操作者（apiAuth 双凭据：管理员 JWT 或 API Token）。
func actorOf(c *gin.Context) string {
	if v, ok := c.Get("apiUser"); ok {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	if v, ok := c.Get("adminUserID"); ok {
		if s, _ := v.(string); s != "" {
			return s
		}
	}
	return "unknown"
}

// listEnrollmentTokens 凭据列表（GET /api/enrollment-tokens）：token 列带
// json:"-" 不回明文，只看状态（待用/已用/已吊销/过期）。
func (h *Handler) listEnrollmentTokens(c *gin.Context) {
	out := []storage.EnrollmentToken{}
	if err := h.db.Order("id DESC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// revokeEnrollmentToken 吊销未用凭据（DELETE /api/enrollment-tokens/:id）：
// 已消费的是终态（节点已建，吊凭据无意义）回 409；重复吊销幂等 200。
func (h *Handler) revokeEnrollmentToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		fail(c, http.StatusBadRequest, errors.New("invalid id"))
		return
	}
	var e storage.EnrollmentToken
	if err := h.db.First(&e, id).Error; err != nil {
		replyFind(c, err)
		return
	}
	if e.UsedAt != nil {
		fail(c, http.StatusConflict, errors.New("凭据已被使用，不能吊销"))
		return
	}
	if e.RevokedAt == nil {
		now := time.Now()
		if err := h.db.Model(&e).Update("revoked_at", now).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": e.ID, "revoked": true})
}

// enrollNode 凭据换节点（POST /api/enroll，公开面，自带注册凭据）：
// 校验载荷→事务内原子消费凭据+建节点→返回与 POST /api/nodes 同形状的节点
// （含接入令牌，安装脚本解析口径不变）。
func (h *Handler) enrollNode(c *gin.Context) {
	ip := c.ClientIP()
	if !h.enrollLimiter.Allow(ip) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "尝试过于频繁，请稍后再试"})
		return
	}
	var in model.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateNodeInput(&in, true); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	n, err := h.enrollTx(bearerToken(c), &in, time.Now())
	if err != nil {
		if errors.Is(err, errEnrollTokenInvalid) {
			h.enrollLimiter.RecordFailure(ip)
			fail(c, http.StatusUnauthorized, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.enrollLimiter.Reset(ip)
	c.JSON(http.StatusCreated, n)
}

// errEnrollTxAbort 消费失败（凭据不可用）的事务哨兵：与建节点失败（返回原
// 错误）区分，前者 401 后者 500。
var errEnrollTxAbort = errors.New("enroll token not consumable")

// enrollTx 一次性消费：凭据条件更新（未用/未吊销/未过期才中）+ 建节点同
// 事务，任一步失败整体回滚——重放与并发双花都进不了库（redeemTx 同款）。
func (h *Handler) enrollTx(tokenStr string, in *model.NodeInput, now time.Time) (*storage.Node, error) {
	if tokenStr == "" {
		return nil, errEnrollTokenInvalid
	}
	var n *storage.Node
	err := h.db.Transaction(func(tx *gorm.DB) error {
		var e storage.EnrollmentToken
		if err := tx.Where("token = ?", tokenStr).First(&e).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errEnrollTxAbort
			}
			return err
		}
		res := tx.Model(&storage.EnrollmentToken{}).
			Where("id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?", e.ID, now).
			Updates(map[string]any{"used_at": now, "used_by": in.Name})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errEnrollTxAbort
		}
		created, err := newPersistedNode(tx, in)
		if err != nil {
			return err
		}
		n = created
		return nil
	})
	if err != nil {
		if errors.Is(err, errEnrollTxAbort) {
			return nil, errEnrollTokenInvalid
		}
		return nil, err
	}
	return n, nil
}
