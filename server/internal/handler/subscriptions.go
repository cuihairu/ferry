// 自建订阅自助面（ferry-sub-filters，2026-11-21 用户拍板）：用户以订阅令牌
// 登录（panelUser 同口径）管理自己的独立订阅——CRUD 与重置 token。
// 路径与管理面共用 /api 前缀，但鉴权口径是订阅令牌（panelAuth），不是管理员 JWT。
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

// subPerUserMax 是每用户自建订阅上限（超限 409）。
const subPerUserMax = 10

// ctxSubUserKey 是 panelAuth 中间件存入上下文的键（当前登录用户）。
const ctxSubUserKey = "subscriptions.user"

// panelAuth 是订阅自助面的身份中间件：解析订阅令牌（同 panelUser 口径），
// 成功则把用户存入上下文供 handler 读取；失败由 panelUser 自行回 404（防枚举）。
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

// subUserFrom 从上下文取当前登录用户（panelAuth 之后可用）。
func subUserFrom(c *gin.Context) (storage.User, bool) {
	v, ok := c.Get(ctxSubUserKey)
	if !ok {
		return storage.User{}, false
	}
	u, ok := v.(storage.User)
	return u, ok
}

// decodeStrict 严格解码请求体：未知字段与格式错误一律报错——防「nam」这类
// 拼写错误被静默忽略成空 name，导致看似配置了实则失效。
func decodeStrict(c *gin.Context, out any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

// filterText 规范化 filter 载荷为落库文本：缺省 "{}"，提供时严格校验
// （解析失败或含未知字段即报错，由调用方转 400）。
func filterText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if _, err := sub.ParseFilter(string(raw)); err != nil {
		return "", fmt.Errorf("invalid filter: %w", err)
	}
	return string(raw), nil
}

// configText 规范化 config 载荷为落库文本（口径同 filterText）。
func configText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	if _, err := sub.ParseConfig(string(raw)); err != nil {
		return "", fmt.Errorf("invalid config: %w", err)
	}
	return string(raw), nil
}

// ownSubscription 按 id + 属主查自建订阅：他人 id 或不存在都查不到，
// handler 一律回 404（不泄露「存在但不是你的」，与令牌防枚举口径一致）。
func (h *Handler) ownSubscription(id uint, userID uint) (storage.Subscription, error) {
	var s storage.Subscription
	err := h.db.Where("id = ? AND user_id = ?", id, userID).First(&s).Error
	return s, err
}

// listSubscriptions 列出当前用户的全部自建订阅（GET /api/subscriptions）。
// token 明文随列表返回——是本人自己的订阅，可以看。
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

// createSubscription 创建自建订阅（POST /api/subscriptions）：
// token 服务端 crypto/rand 生成；name 必填；filter/config 严格 JSON（未知字段 400）；
// 每用户上限 subPerUserMax，超限 409。
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

// updateSubscription 更新自建订阅（PATCH /api/subscriptions/:id）：只能改自己的，
// 他人 id 404；可选 name/filter/config/enabled，缺省保持原值；无任何可改字段
// 时原样返回当前值（幂等）。
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
		// map 更新不按零值跳过，enabled=false 可写（struct 更新会漏掉 false）。
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

// deleteSubscription 删除自建订阅（DELETE /api/subscriptions/:id）：
// 只能删自己的，他人 id 404。
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

// resetSubscriptionToken 重新生成订阅令牌（POST /api/subscriptions/:id/reset-token）：
// 泄漏换发用，旧令牌立即失效；只能操作自己的。
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

// subIDParam 解析 :id 路由参数为订阅 id（非法值 400）。
func subIDParam(c *gin.Context) (uint, error) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(id), nil
}
