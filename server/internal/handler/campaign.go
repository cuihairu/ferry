package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 限时活动（PROMO-2，运营设计 §5）：campaigns 表 + 管理面 CRUD + panel
// 活动位 + 下单自动适用。取优口径同优惠码（DS §3.3）：一单一优惠源——
// 码与活动至多取一，择惠大者；首单判定=无历史已付订单，续费=命中已有
// 已付订单再次下单。

// campaignRules 是活动折扣规则（rules JSON）：与优惠码同一 cut/pct 口径。
type campaignRules struct {
	Kind      string `json:"kind"`
	Value     int64  `json:"value"`
	Scope     string `json:"scope"`
	MinAmount int64  `json:"min_amount"`
}

// parseCampaignRules 解析并校验活动规则 JSON（口径同 validateDiscountRules）。
// 兼容两种下发形态：JSON 对象与 JSON 字符串（RawMessage 绑字符串会带引号）。
func (h *Handler) parseCampaignRules(raw string) (campaignRules, error) {
	var r campaignRules
	s := strings.TrimSpace(raw)
	if len(s) > 0 && s[0] == '"' {
		var inner string
		if err := json.Unmarshal([]byte(s), &inner); err != nil {
			return r, errors.New("rules must be valid JSON")
		}
		s = inner
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, errors.New("rules must be valid JSON")
	}
	scope, err := h.validateDiscountRules(r.Kind, r.Value, r.Scope, r.MinAmount)
	if err != nil {
		return r, err
	}
	r.Scope = scope
	return r, nil
}

// campaignInput 是建/改活动载荷（整包更新）。rules 收 JSON 字符串或对象
// （RawMessage 两种形态均可绑），校验与存储统一走 parseCampaignRules。
type campaignInput struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"` // first_order / renew / timed
	Rules    json.RawMessage `json:"rules"`
	StartsAt *time.Time      `json:"starts_at"`
	EndsAt   *time.Time      `json:"ends_at"`
	Enabled  *bool           `json:"enabled"`
}

// listCampaigns 活动列表（GET /api/campaigns）：id DESC，dash 全量管理。
func (h *Handler) listCampaigns(c *gin.Context) {
	var rows []storage.Campaign
	if err := h.db.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// validateCampaignInput 校验活动载荷：名称、kind 枚举、rules JSON 口径、
// 起止必填且 end 在 start 之后。
func (h *Handler) validateCampaignInput(in *campaignInput) error {
	if in.Name == "" {
		return errors.New("name is required")
	}
	if in.Kind != "first_order" && in.Kind != "renew" && in.Kind != "timed" {
		return errors.New("kind must be first_order, renew or timed")
	}
	if _, err := h.parseCampaignRules(string(in.Rules)); err != nil {
		return err
	}
	if in.StartsAt == nil || in.EndsAt == nil {
		return errors.New("starts_at and ends_at are required")
	}
	if !in.EndsAt.After(*in.StartsAt) {
		return errors.New("ends_at must be after starts_at")
	}
	return nil
}

// createCampaign 新建活动（POST /api/campaigns）。
func (h *Handler) createCampaign(c *gin.Context) {
	var in campaignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.validateCampaignInput(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row := storage.Campaign{
		Name: in.Name, Kind: in.Kind, Rules: string(in.Rules),
		StartsAt: *in.StartsAt, EndsAt: *in.EndsAt, Enabled: true,
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, row)
}

// updateCampaign 编辑活动（PUT /api/campaigns/:id）：整包更新。
func (h *Handler) updateCampaign(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	var row storage.Campaign
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, errors.New("campaign not found"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	var in campaignInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := h.validateCampaignInput(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row.Name, row.Kind, row.Rules = in.Name, in.Kind, string(in.Rules)
	row.StartsAt, row.EndsAt = *in.StartsAt, *in.EndsAt
	if in.Enabled != nil {
		row.Enabled = *in.Enabled
	}
	if err := h.db.Save(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteCampaign 删除活动（DELETE /api/campaigns/:id）：已核销订单的快照
// 留档不受影响（promo_snapshot 自含明细）。
func (h *Handler) deleteCampaign(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	res := h.db.Delete(&storage.Campaign{}, uint(id))
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		fail(c, http.StatusNotFound, errors.New("campaign not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// panelCampaigns 活动位（GET /api/panel/campaigns，PROMO-2）：进行中与
// 即将开始的活动（含折扣规则明细供展示）；过期/停用不下发。
func (h *Handler) panelCampaigns(c *gin.Context) {
	if _, ok := h.panelUser(c); !ok {
		return
	}
	now := time.Now()
	var rows []storage.Campaign
	if err := h.db.Where("enabled = ? AND ends_at > ?", true, now).
		Order("starts_at").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		rules, err := h.parseCampaignRules(rows[i].Rules)
		if err != nil {
			continue
		}
		state := "upcoming"
		if !now.Before(rows[i].StartsAt) {
			state = "active"
		}
		out = append(out, gin.H{
			"id": rows[i].ID, "name": rows[i].Name, "kind": rows[i].Kind,
			"state": state, "starts_at": rows[i].StartsAt, "ends_at": rows[i].EndsAt,
			"discount_kind": rules.Kind, "discount_value": rules.Value,
			"scope": rules.Scope, "min_amount": rules.MinAmount,
		})
	}
	c.JSON(http.StatusOK, out)
}

// campaignEligible 判定活动对当前用户是否可参与：enabled、在窗内、
// 首单/续费按历史已付订单判定（timed 恒可）。
func (h *Handler) campaignEligible(tx *gorm.DB, c *storage.Campaign, u *storage.User, now time.Time) (bool, error) {
	if !c.Enabled || now.Before(c.StartsAt) || !now.Before(c.EndsAt) {
		return false, nil
	}
	if c.Kind == "timed" {
		return true, nil
	}
	var paid int64
	if err := tx.Model(&storage.PaymentOrder{}).
		Where("user_id = ? AND status = ?", u.ID, "paid").
		Count(&paid).Error; err != nil {
		return false, err
	}
	if c.Kind == "first_order" {
		return paid == 0, nil
	}
	return paid > 0, nil // renew
}

// bestCampaign 在窗口内且适用该商品的活动里取惠大者（一单一优惠源，
// 活动内部亦不叠加）。无命中返回 nil。
func (h *Handler) bestCampaign(tx *gorm.DB, u *storage.User, batch *storage.CardBatch, price int64, now time.Time) (*storage.Campaign, int64, error) {
	var rows []storage.Campaign
	if err := tx.Where("enabled = ? AND starts_at <= ? AND ends_at > ?", true, now, now).
		Order("id").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	var best *storage.Campaign
	bestPaid := price
	for i := range rows {
		c := &rows[i]
		ok, err := h.campaignEligible(tx, c, u, now)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			continue
		}
		rules, err := h.parseCampaignRules(c.Rules)
		if err != nil {
			continue
		}
		if rules.Scope != "all" {
			id, err := parseBatchScope(rules.Scope)
			if err != nil || id != batch.ID {
				continue
			}
		}
		if rules.MinAmount > 0 && price < rules.MinAmount {
			continue
		}
		paid := couponDiscount(rules.Kind, rules.Value, price)
		if paid < bestPaid {
			best, bestPaid = c, paid
		}
	}
	return best, bestPaid, nil
}

// campaignSnapshot 活动快照（订单落 promo_snapshot）：活动/规则/原价/
// 折扣/实付，活动删除后明细仍可对账。
func campaignSnapshot(c *storage.Campaign, rules campaignRules, list, paid int64) string {
	b, _ := json.Marshal(map[string]any{
		"source": "campaign", "campaign_id": c.ID, "name": c.Name,
		"kind": rules.Kind, "value": rules.Value,
		"list_amount_cents": list, "discount_cents": list - paid, "amount_cents": paid,
	})
	return string(b)
}
