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

// Time-limited campaigns (PROMO-2, operations design §5): campaigns table +
// admin-side CRUD + panel campaign slot + automatic application at order time.
// Discount selection follows the same convention as coupon codes (DS §3.3):
// one discount source per order — a code and a campaign pick at most one,
// whichever saves more; first order means no historical paid order, renew means
// an existing paid order and ordering again.

// campaignRules is a campaign discount rule set (rules JSON): same cut/pct convention as coupon codes.
type campaignRules struct {
	Kind      string `json:"kind"`
	Value     int64  `json:"value"`
	Scope     string `json:"scope"`
	MinAmount int64  `json:"min_amount"`
}

// parseCampaignRules parses and validates the campaign rules JSON (same
// convention as validateDiscountRules). Two delivered shapes are supported: a
// JSON object and a JSON string (RawMessage binding keeps the quotes for strings).
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

// campaignInput is the create/update campaign payload (full replacement).
// rules accepts a JSON string or object (RawMessage binds both), and validation
// and storage both go through parseCampaignRules.
type campaignInput struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"` // first_order / renew / timed
	Rules    json.RawMessage `json:"rules"`
	StartsAt *time.Time      `json:"starts_at"`
	EndsAt   *time.Time      `json:"ends_at"`
	Enabled  *bool           `json:"enabled"`
}

// listCampaigns lists campaigns (GET /api/campaigns): id DESC, full dash-side management.
func (h *Handler) listCampaigns(c *gin.Context) {
	var rows []storage.Campaign
	if err := h.db.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// validateCampaignInput validates the campaign payload: name, kind enum,
// rules JSON conventions, required start/end with end after start.
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

// createCampaign creates a campaign (POST /api/campaigns).
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

// updateCampaign edits a campaign (PUT /api/campaigns/:id): full replacement.
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

// deleteCampaign deletes a campaign (DELETE /api/campaigns/:id): snapshots
// already written for redeemed orders stay intact for the record
// (promo_snapshot carries its own details).
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

// panelCampaigns serves the campaign slot (GET /api/panel/campaigns, PROMO-2):
// running and upcoming campaigns (including discount rule details for display);
// expired or disabled ones are never sent down.
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

// campaignEligible decides whether a campaign applies to the current user:
// enabled, inside the window, with first_order/renew judged from historical
// paid orders (timed always applies).
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

// bestCampaign picks the biggest saving among campaigns inside the window that
// apply to the product (one discount source per order, and campaigns never
// stack with each other either). Returns nil on no match.
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

// campaignSnapshot builds the campaign snapshot (stored on the order as
// promo_snapshot): campaign/rules/list price/discount/amount paid, so details
// stay reconcilable after the campaign is deleted.
func campaignSnapshot(c *storage.Campaign, rules campaignRules, list, paid int64) string {
	b, _ := json.Marshal(map[string]any{
		"source": "campaign", "campaign_id": c.ID, "name": c.Name,
		"kind": rules.Kind, "value": rules.Value,
		"list_amount_cents": list, "discount_cents": list - paid, "amount_cents": paid,
	})
	return string(b)
}
