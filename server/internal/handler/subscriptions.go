// Self-service surface for user-built subscriptions (ferry-sub-filters, decided by
// the user on 2026-11-21): users log in with their subscription token (same code path
// as panelUser) to manage their own subscriptions — CRUD plus token reset.
// Paths share the /api prefix with the admin surface, but authentication uses the
// subscription token (panelAuth), not the admin JWT.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// subPerUserMax is the per-user cap on self-built subscriptions (exceeding it gives 409).
const subPerUserMax = 10

// ctxSubUserKey is the key the panelAuth middleware stores the logged-in user under.
const ctxSubUserKey = "subscriptions.user"

// panelAuth is the identity middleware for the subscription self-service surface:
// it resolves the subscription token (same code path as panelUser) and, on success,
// stores the user in the context for handlers to read; on failure panelUser itself
// answers 404 (anti-enumeration).
func (h *Handler) panelAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := h.panelUser(c)
		if !ok {
			c.Abort()
			return
		}
		c.Set(ctxSubUserKey, u)
		c.Next()
	}
}

// subUserFrom reads the logged-in user from the context (available after panelAuth).
func subUserFrom(c *gin.Context) (storage.User, bool) {
	v, ok := c.Get(ctxSubUserKey)
	if !ok {
		return storage.User{}, false
	}
	u, ok := v.(storage.User)
	return u, ok
}

// decodeStrict strictly decodes the request body: unknown fields and malformed
// payloads always error out — prevents a typo like "nam" from being silently
// ignored into an empty name that looks configured but does nothing.
func decodeStrict(c *gin.Context, out any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// filterText normalizes the filter payload into stored text: "{}" when absent,
// strict validation when provided (parse failures or unknown fields error out;
// the caller maps that to 400).
func filterText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if _, err := sub.ParseFilter(string(raw)); err != nil {
		return "", fmt.Errorf("invalid filter: %w", err)
	}
	return string(raw), nil
}

// configText normalizes the config payload into stored text (same policy as filterText).
func configText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if _, err := sub.ParseConfig(string(raw)); err != nil {
		return "", fmt.Errorf("invalid config: %w", err)
	}
	return string(raw), nil
}

// ownSubscription looks up a self-built subscription by id + owner: another user's
// id and a nonexistent id both miss, and the handler answers 404 either way (never
// leaking "exists but not yours", consistent with the token anti-enumeration policy).
func (h *Handler) ownSubscription(id uint, userID uint) (storage.Subscription, error) {
	var s storage.Subscription
	err := h.db.Where("id = ? AND user_id = ?", id, userID).First(&s).Error
	return s, err
}

// listSubscriptions lists all of the current user's self-built subscriptions
// (GET /api/subscriptions). Tokens are returned in plaintext — these are the
// user's own subscriptions, so seeing them is fine.
func (h *Handler) listSubscriptions(c *gin.Context) {
	u, ok := subUserFrom(c)
	if !ok {
		return
	}
	var subs []storage.Subscription
	if err := h.db.Where("user_id = ?", u.ID).Order("id").Find(&subs).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, subs)
}

// createSubscription creates a self-built subscription (POST /api/subscriptions):
// the token is generated server-side with crypto/rand; name is required; filter/config
// are strict JSON (unknown fields give 400); the per-user cap is subPerUserMax,
// exceeding it gives 409.
func (h *Handler) createSubscription(c *gin.Context) {
	u, ok := subUserFrom(c)
	if !ok {
		return
	}
	var in model.SubCreateInput
	if err := decodeStrict(c, &in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" {
		fail(c, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	filter, err := filterText(in.Filter)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	config, err := configText(in.Config)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var count int64
	if err := h.db.Model(&storage.Subscription{}).Where("user_id = ?", u.ID).Count(&count).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if count >= subPerUserMax {
		fail(c, http.StatusConflict, fmt.Errorf("subscription limit reached (%d)", subPerUserMax))
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	s := storage.Subscription{
		UserID: u.ID, Token: token, Name: in.Name,
		Filter: filter, Config: config, Enabled: true,
	}
	if err := h.db.Create(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, s)
}

// updateSubscription updates a self-built subscription (PATCH /api/subscriptions/:id):
// only one's own — another user's id gives 404; name/filter/config/enabled are optional
// and default to keeping the original values; with no updatable fields at all it
// returns the current value unchanged (idempotent).
func (h *Handler) updateSubscription(c *gin.Context) {
	u, ok := subUserFrom(c)
	if !ok {
		return
	}
	id, err := subIDParam(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	s, err := h.ownSubscription(id, u.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var in model.SubUpdateInput
	if err := decodeStrict(c, &in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if in.Name != nil {
		if *in.Name == "" {
			fail(c, http.StatusBadRequest, errors.New("name is required"))
			return
		}
		updates["name"] = *in.Name
	}
	if in.Filter != nil {
		filter, err := filterText(*in.Filter)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updates["filter"] = filter
	}
	if in.Config != nil {
		config, err := configText(*in.Config)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updates["config"] = config
	}
	if in.Enabled != nil {
		// map-based updates don't skip zero values, so enabled=false is writable
		// (a struct update would drop false).
		updates["enabled"] = *in.Enabled
	}
	if len(updates) == 0 {
		c.JSON(http.StatusOK, s)
		return
	}
	if err := h.db.Model(&s).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, s)
}

// deleteSubscription deletes a self-built subscription (DELETE /api/subscriptions/:id):
// only one's own — another user's id gives 404.
func (h *Handler) deleteSubscription(c *gin.Context) {
	u, ok := subUserFrom(c)
	if !ok {
		return
	}
	id, err := subIDParam(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	s, err := h.ownSubscription(id, u.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Delete(&s).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// resetSubscriptionToken regenerates the subscription token
// (POST /api/subscriptions/:id/reset-token): for leak re-issuance — the old token
// is invalidated immediately; only one's own subscription.
func (h *Handler) resetSubscriptionToken(c *gin.Context) {
	u, ok := subUserFrom(c)
	if !ok {
		return
	}
	id, err := subIDParam(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	s, err := h.ownSubscription(id, u.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	token, err := RandomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.db.Model(&s).Update("token", token).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	s.Token = token
	c.JSON(http.StatusOK, s)
}

// subIDParam parses the :id route param as a subscription id (invalid values give 400).
func subIDParam(c *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(id), nil
}
