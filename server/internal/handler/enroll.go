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

// One-time node enrollment credential (FERRY-001): the admin plane /api/nodes
// is guarded by apiAuth, but an install script doing a first install should not
// hold admin credentials, and anonymous node creation must stay closed. Flow:
// an admin issues a short-lived one-time credential on the dash/API side
// (POST /api/enrollment-tokens, through apiAuth+audit) -> the node host carries
// it via --enroll-token to the public-plane POST /api/enroll to exchange for a
// node token -> the credential is void once used, so a leak's blast radius is
// registering one node (revocable and attributable: the row carries the issuer
// and consumption traces, and the public plane bypasses auditMiddleware, so this
// row is itself the audit carrier).

const (
	enrollTTLDefaultMinutes = 60
	enrollTTLMaxMinutes     = 1440
)

// errEnrollTokenInvalid covers all four states missing/expired/used/revoked:
// one uniform 401 message, so a prober cannot distinguish credential states.
var errEnrollTokenInvalid = errors.New("注册凭据无效或已过期")

// createEnrollmentToken issues a one-time enrollment credential (POST
// /api/enrollment-tokens, admin data plane): the token plaintext appears only
// in this response; ttl defaults to 60 minutes, capped at one day.
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

// actorOf returns the current operator (apiAuth carries two credential kinds:
// admin JWT or API token).
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

// listEnrollmentTokens credential listing (GET /api/enrollment-tokens): the
// token column carries json:"-" so no plaintext is returned; only states are
// visible (unused/used/revoked/expired).
func (h *Handler) listEnrollmentTokens(c *gin.Context) {
	out := []storage.EnrollmentToken{}
	if err := h.db.Order("id DESC").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// revokeEnrollmentToken revokes an unused credential (DELETE
// /api/enrollment-tokens/:id): an already-consumed one is a terminal state
// (the node exists, revoking the credential is pointless) and returns 409;
// a repeat revoke is idempotent 200.
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

// enrollNode exchanges a credential for a node (POST /api/enroll, public
// plane, carries its own enrollment credential): validate payload -> atomically
// consume the credential and create the node in one transaction -> return a node
// in the same shape as POST /api/nodes (including the access token, so the
// install script's parsing contract is unchanged).
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

// errEnrollTxAbort is the transaction sentinel for consumption failure
// (credential unusable): it distinguishes that from a node-creation failure
// (which returns the original error) -- the former is 401, the latter 500.
var errEnrollTxAbort = errors.New("enroll token not consumable")

// enrollTx is the one-time consumption: a conditional credential update (only
// hits when unused/unrevoked/unexpired) plus node creation in the same
// transaction, rolling back entirely if any step fails -- neither replay nor
// concurrent double-spend can reach the database (same pattern as redeemTx).
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
