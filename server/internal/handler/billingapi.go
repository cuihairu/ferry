package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/billing"
	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Billing API (§4.1~§4.7): the vendor's actual bills are separate from Ferry
// estimates; every import run, allocation rule and reconciliation decision is
// persisted and visible under the admin plane.

func (h *Handler) billingService() *billing.Service {
	return billing.New(h.db, h.secrets, nil)
}

// billingActor resolves the operator identity the same way the cloud
// operation handler does.
func (h *Handler) billingActor(c *gin.Context) string {
	actor, _ := h.operActor(c)
	return actor
}

// listCostSources lists cost data sources (F-0502).
func (h *Handler) listCostSources(c *gin.Context) {
	var rows []storage.CostProvider
	if err := h.db.Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sources": rows})
}

// createCostSource registers one cost source (F-0502/F-0522). A source can be
// tied to a cloud Provider row for API pulls or left unlinked for manual bills.
func (h *Handler) createCostSource(c *gin.Context) {
	var in struct {
		Name       string `json:"name"`
		ProviderID *uint  `json:"provider_id"`
		Source     string `json:"source"`
		Account    string `json:"account"`
		Currency   string `json:"currency"`
		Region     string `json:"region"`
		Note       string `json:"note"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" || !billing.ValidSource(in.Source) || in.Currency == "" {
		fail(c, http.StatusBadRequest, errors.New("name, source and currency are required"))
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	row := storage.CostProvider{
		Name: in.Name, ProviderID: in.ProviderID, Source: in.Source,
		Account: in.Account, Currency: in.Currency, Region: in.Region,
		Note: in.Note, Enabled: enabled,
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

func (h *Handler) updateCostSource(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cost source not found"))
		return
	}
	var row storage.CostProvider
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in struct {
		Name    *string `json:"name"`
		Account *string `json:"account"`
		Region  *string `json:"region"`
		Note    *string `json:"note"`
		Enabled *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if in.Name != nil {
		updates["name"] = *in.Name
	}
	if in.Account != nil {
		updates["account"] = *in.Account
	}
	if in.Region != nil {
		updates["region"] = *in.Region
	}
	if in.Note != nil {
		updates["note"] = *in.Note
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if len(updates) > 0 {
		if err := h.db.Model(&row).Updates(updates).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// listBillRecords exposes the actual vendor bills, filterable by date,
// source, account, region and resource (F-0636).
func (h *Handler) listBillRecords(c *gin.Context) {
	q := h.db.Model(&storage.CostRecord{}).Order("incurred_at DESC, id DESC")
	if s := c.Query("cost_provider_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid cost_provider_id"))
			return
		}
		q = q.Where("cost_provider_id = ?", id)
	}
	if s := c.Query("source"); s != "" {
		q = q.Where("source = ?", s)
	}
	if s := c.Query("account"); s != "" {
		q = q.Where("account = ?", s)
	}
	if s := c.Query("region"); s != "" {
		q = q.Where("region = ?", s)
	}
	if s := c.Query("from"); s != "" {
		q = q.Where("incurred_at >= ?", s)
	}
	if s := c.Query("to"); s != "" {
		q = q.Where("incurred_at <= ?", s)
	}
	var rows []storage.CostRecord
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"records": rows})
}

// listBillItems lists line items of one bill record (F-0529/F-0533).
func (h *Handler) listBillItems(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("bill record not found"))
		return
	}
	var rows []storage.CostItem
	if err := h.db.Where("cost_record_id = ?", id).Order("id ASC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// importBillsFromAPI pulls a billing window from a source's cloud provider
// (F-0524~F-0528). The source must be linked to a Provider row.
func (h *Handler) importBillsFromAPI(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cost source not found"))
		return
	}
	var in struct {
		From        string `json:"from"`
		To          string `json:"to"`
		Account     string `json:"account"`
		Region      string `json:"region"`
		Trigger     string `json:"trigger"`
		PageSize    int    `json:"page_size"`
		Page        int    `json:"page"`
		Incremental bool   `json:"incremental"`
	}
	_ = c.ShouldBindJSON(&in)
	from, err := time.Parse(time.RFC3339, in.From)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("from must be RFC3339"))
		return
	}
	to := time.Time{}
	if in.To != "" {
		to, err = time.Parse(time.RFC3339, in.To)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("to must be RFC3339"))
			return
		}
	}
	actor := h.billingActor(c)
	run, err := h.billingService().ImportFromAPI(c.Request.Context(), billing.ImportRequest{
		CostProviderID: uint(id), Window: cloudprov.BillingWindow{From: from, To: to},
		Account: in.Account, Region: in.Region, Trigger: in.Trigger,
		PageSize: in.PageSize, Page: in.Page, Incremental: in.Incremental,
	}, actor, time.Now())
	if err != nil {
		if run != nil && (run.State == billing.RunFailed || run.State == billing.RunPartial) {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": run.Error, "run": run})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "run": run})
}

// importBillsFromFile imports a hand-uploaded JSON bill file (F-0523/F-0534).
// Uploads are multipart with a `file` field; the raw bytes are snapshotted on
// each stored record for traceability.
func (h *Handler) importBillsFromFile(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cost source not found"))
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("file is required"))
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	run, err := h.billingService().ImportFile(uint(id), raw, "json", h.billingActor(c), time.Now())
	if err != nil {
		if run != nil && (run.State == billing.RunFailed || run.State == billing.RunPartial) {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": run.Error, "run": run})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "run": run})
}

// listBillImports exposes import runs and their failure/retry state (F-0532/
// F-0533).
func (h *Handler) listBillImports(c *gin.Context) {
	q := h.db.Model(&storage.BillImport{}).Order("id DESC")
	if s := c.Query("cost_provider_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid cost_provider_id"))
			return
		}
		q = q.Where("cost_provider_id = ?", id)
	}
	var rows []storage.BillImport
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"imports": rows})
}

// listAllocationRules lists versioned rules (F-0568).
func (h *Handler) listAllocationRules(c *gin.Context) {
	var rows []storage.CostAllocationRule
	if err := h.db.Order("id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": rows})
}

// createAllocationRule creates one rule version 1 (F-0563~F-0567).
func (h *Handler) createAllocationRule(c *gin.Context) {
	var in struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Params  string `json:"params"`
		Note    string `json:"note"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Name == "" || !billing.ValidBasis(in.Kind) {
		fail(c, http.StatusBadRequest, errors.New("name and valid kind are required"))
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	row := storage.CostAllocationRule{
		Name: in.Name, Kind: in.Kind, Params: in.Params, Note: in.Note,
		Version: 1, Enabled: enabled,
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// editAllocationRule appends a new rule version (F-0567/F-0568).
func (h *Handler) editAllocationRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("rule not found"))
		return
	}
	var in struct {
		Params string `json:"params"`
		Note   string `json:"note"`
	}
	_ = c.ShouldBindJSON(&in)
	next, err := h.billingService().NewVersion(uint(id), billing.ParseRuleParams(in.Params), in.Note, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, next)
}

// allocateItem splits one bill item under a rule version (F-0562~F-0570).
func (h *Handler) allocateItem(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("bill item not found"))
		return
	}
	var in struct {
		RuleID  uint `json:"rule_id"`
		Version int  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rows, err := h.billingService().Allocate(uint(id), in.RuleID, in.Version, time.Now())
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"allocations": rows})
}

// recomputeAllocations reruns a rule version over a bill record (F-0571).
func (h *Handler) recomputeAllocations(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("bill record not found"))
		return
	}
	var in struct {
		RuleID  uint `json:"rule_id"`
		Version int  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	count, err := h.billingService().Recompute(uint(id), in.RuleID, in.Version, time.Now())
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// listAllocations lists allocation results (F-0569).
func (h *Handler) listAllocations(c *gin.Context) {
	q := h.db.Model(&storage.CostAllocation{}).Order("id DESC")
	if s := c.Query("cost_item_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid cost_item_id"))
			return
		}
		q = q.Where("cost_item_id = ?", id)
	}
	if s := c.Query("target_kind"); s != "" {
		q = q.Where("target_kind = ?", s)
	}
	var rows []storage.CostAllocation
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"allocations": rows})
}

// listEstimates exposes the internal estimates, always separate from bills
// (F-0552/F-0629).
func (h *Handler) listEstimates(c *gin.Context) {
	q := h.db.Order("period_end DESC, id DESC")
	if s := c.Query("node_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid node_id"))
			return
		}
		q = q.Where("node_id = ?", id)
	}
	var rows []storage.CostEstimate
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"estimates": rows})
}

// listCostLines lists a node's recurring cost lines (F-0545~F-0549).
func (h *Handler) listCostLines(c *gin.Context) {
	q := h.db.Order("id ASC")
	if s := c.Query("node_id"); s != "" {
		id, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid node_id"))
			return
		}
		q = q.Where("node_id = ?", id)
	}
	var rows []storage.NodeCostLine
	if err := q.Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"lines": rows})
}

// createCostLine adds one manual cost line to a node (F-0542/F-0545~F-0549).
// Hourly lines carry hourly_price_cents, everything else a monthly amount;
// only discount lines may be negative, and an unknown price is stored marked.
func (h *Handler) createCostLine(c *gin.Context) {
	var in struct {
		NodeID           uint   `json:"node_id"`
		Kind             string `json:"kind"`
		HourlyPriceCents int64  `json:"hourly_price_cents"`
		AmountCents      int64  `json:"amount_cents"`
		UnknownPrice     bool   `json:"unknown_price"`
		Currency         string `json:"currency"`
		Note             string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if !billing.ValidLineKind(in.Kind) {
		fail(c, http.StatusBadRequest, errors.New("invalid line kind"))
		return
	}
	var node storage.Node
	if err := h.db.First(&node, in.NodeID).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("node not found"))
		return
	}
	if in.AmountCents < 0 && in.Kind != billing.KindDiscount {
		fail(c, http.StatusBadRequest, errors.New("only discount lines may be negative"))
		return
	}
	if in.Currency == "" {
		in.Currency = node.Currency
	}
	if in.Currency == "" {
		in.Currency = "CNY"
	}
	row := storage.NodeCostLine{
		NodeID: in.NodeID, Kind: in.Kind, HourlyPriceCents: in.HourlyPriceCents,
		AmountCents: in.AmountCents, UnknownPrice: in.UnknownPrice,
		Currency: in.Currency, Note: in.Note, Enabled: true,
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// updateCostLine edits a cost line; recomputation picks the change up.
func (h *Handler) updateCostLine(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cost line not found"))
		return
	}
	var row storage.NodeCostLine
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in struct {
		HourlyPriceCents *int64  `json:"hourly_price_cents"`
		AmountCents      *int64  `json:"amount_cents"`
		UnknownPrice     *bool   `json:"unknown_price"`
		Note             *string `json:"note"`
		Enabled          *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	updates := map[string]any{}
	if in.HourlyPriceCents != nil {
		updates["hourly_price_cents"] = *in.HourlyPriceCents
	}
	if in.AmountCents != nil {
		if *in.AmountCents < 0 && row.Kind != billing.KindDiscount {
			fail(c, http.StatusBadRequest, errors.New("only discount lines may be negative"))
			return
		}
		updates["amount_cents"] = *in.AmountCents
	}
	if in.UnknownPrice != nil {
		updates["unknown_price"] = *in.UnknownPrice
	}
	if in.Note != nil {
		updates["note"] = *in.Note
	}
	if in.Enabled != nil {
		updates["enabled"] = *in.Enabled
	}
	if len(updates) > 0 {
		if err := h.db.Model(&row).Updates(updates).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.db.First(&row, uint(id)).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deleteCostLine removes a cost line.
func (h *Handler) deleteCostLine(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cost line not found"))
		return
	}
	if err := h.db.Delete(&storage.NodeCostLine{}, uint(id)).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// computeEstimates runs the estimate engine for one node or all nodes over a
// period (F-0542~F-0549); rows upsert by scope/kind/period.
func (h *Handler) computeEstimates(c *gin.Context) {
	var in struct {
		NodeID uint   `json:"node_id"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	from, err := time.Parse(time.RFC3339, in.From)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("from must be RFC3339"))
		return
	}
	to := from.AddDate(0, 1, 0)
	if in.To != "" {
		if to, err = time.Parse(time.RFC3339, in.To); err != nil {
			fail(c, http.StatusBadRequest, errors.New("to must be RFC3339"))
			return
		}
	}
	if in.NodeID > 0 {
		var node storage.Node
		if err := h.db.First(&node, in.NodeID).Error; err != nil {
			fail(c, http.StatusNotFound, errors.New("node not found"))
			return
		}
		rows, err := billing.EstimateNode(h.db, &node, from, to)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(rows), "estimates": rows})
		return
	}
	count, err := billing.EstimateAll(h.db, from, to)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

// createEstimate stores one internal estimate with its price source and
// effective time (F-0550). A line with an unknown price is stored marked
// unknown, never with an invented amount.
func (h *Handler) createEstimate(c *gin.Context) {
	var in struct {
		NodeID       *uint  `json:"node_id"`
		PathID       *uint  `json:"path_id"`
		Region       string `json:"region"`
		Kind         string `json:"kind"`
		AmountCents  int64  `json:"amount_cents"`
		Currency     string `json:"currency"`
		PriceSource  string `json:"price_source"`
		PriceAt      string `json:"price_at"`
		UnknownPrice bool   `json:"unknown_price"`
		PeriodStart  string `json:"period_start"`
		PeriodEnd    string `json:"period_end"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Currency == "" || in.PeriodStart == "" || in.PeriodEnd == "" {
		fail(c, http.StatusBadRequest, errors.New("currency, period_start and period_end are required"))
		return
	}
	from, err := time.Parse(time.RFC3339, in.PeriodStart)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("invalid period_start"))
		return
	}
	to, err := time.Parse(time.RFC3339, in.PeriodEnd)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("invalid period_end"))
		return
	}
	if in.UnknownPrice {
		in.AmountCents = 0
	} else if in.AmountCents <= 0 || in.PriceSource == "" {
		fail(c, http.StatusBadRequest, errors.New("amount and price_source are required for a known price"))
		return
	}
	row := storage.CostEstimate{
		NodeID: in.NodeID, PathID: in.PathID, Region: in.Region, Kind: in.Kind,
		AmountCents: in.AmountCents, Currency: in.Currency, PriceSource: in.PriceSource,
		UnknownPrice: in.UnknownPrice, PeriodStart: from, PeriodEnd: to,
		DedupeKey: billing.EstimateKey(in.Kind, in.NodeID, in.PathID, in.Region, from, to),
	}
	if in.PriceAt != "" {
		at, err := time.Parse(time.RFC3339, in.PriceAt)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("invalid price_at"))
			return
		}
		row.PriceAt = &at
	}
	res := h.db.Where("dedupe_key = ?", row.DedupeKey).FirstOrCreate(&row)
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	c.JSON(http.StatusOK, row)
}

// listReconciliations lists estimate-vs-actual comparisons (F-0587).
func (h *Handler) listReconciliations(c *gin.Context) {
	var rows []storage.CostReconciliation
	if err := h.db.Order("period_end DESC, id DESC").Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reconciliations": rows})
}

// createReconciliation builds a comparison row from stored bill totals and
// estimate totals for one scope and period (F-0587/F-0593).
func (h *Handler) createReconciliation(c *gin.Context) {
	var in struct {
		ScopeKind string `json:"scope_kind"`
		TargetID  uint   `json:"target_id"`
		TargetKey string `json:"target_key"`
		From      string `json:"from"`
		To        string `json:"to"`
		Currency  string `json:"currency"`
		Actual    int64  `json:"actual_cents"`
		Estimate  int64  `json:"estimate_cents"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Actual == 0 {
		in.Actual = h.actualSpend(in.ScopeKind, in.TargetID, in.TargetKey, in.From, in.To)
	}
	if in.Estimate == 0 {
		in.Estimate = h.estimatedSpend(in.ScopeKind, in.TargetID, in.TargetKey, in.From, in.To)
	}
	from, err := time.Parse(time.RFC3339, in.From)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("invalid from"))
		return
	}
	to, err := time.Parse(time.RFC3339, in.To)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("invalid to"))
		return
	}
	row, err := h.billingService().Reconcile(storage.CostReconciliation{
		ScopeKind: in.ScopeKind, TargetID: in.TargetID, TargetKey: in.TargetKey,
		PeriodStart: from, PeriodEnd: to, Currency: in.Currency,
		EstimateCents: in.Estimate, ActualCents: in.Actual,
	}, time.Now())
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// confirmReconciliation records the operator confirmation/approval (F-0591/F-0592).
func (h *Handler) confirmReconciliation(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("reconciliation not found"))
		return
	}
	var in struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	row, err := h.billingService().ConfirmReconciliation(uint(id), in.Status, in.Note, h.billingActor(c))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// listSpend aggregates actual bills, allocated costs and estimates along the
// provider/account/region/resource/node/path dimensions (F-0581~F-0586). Each
// bucket is per-currency; period bounds are RFC3339 and optional.
func (h *Handler) listSpend(c *gin.Context) {
	scope := c.Query("scope")
	if !billing.ValidSpendScope(scope) {
		fail(c, http.StatusBadRequest, errors.New("scope must be provider/account/region/resource/node/path"))
		return
	}
	from, to, err := spendPeriod(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rows, err := billing.SpendByScope(h.db, scope, from, to)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"scope": scope, "rows": rows})
}

// listSpendTrend buckets actual spend by day, month or billing cycle
// (F-0593); buckets stay per-currency (F-0594).
func (h *Handler) listSpendTrend(c *gin.Context) {
	bucket := c.Query("bucket")
	if bucket == "" {
		bucket = "month"
	}
	from, to, err := spendPeriod(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rows, err := billing.SpendTrend(h.db, bucket, from, to)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bucket": bucket, "rows": rows})
}

// listAnomalies reviews the period for missing bills, unmatched resources and
// spend spikes (F-0588~F-0590); optional growth_pct sets the spike threshold
// (default 100%).
func (h *Handler) listAnomalies(c *gin.Context) {
	from, to, err := spendPeriod(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if from == nil {
		fail(c, http.StatusBadRequest, errors.New("from is required"))
		return
	}
	if to == nil {
		to = &time.Time{}
	}
	spike := c.Query("growth_pct")
	pct := int64(0)
	if spike != "" {
		if _, err := fmt.Sscanf(spike, "%d", &pct); err != nil {
			fail(c, http.StatusBadRequest, errors.New("growth_pct must be an integer"))
			return
		}
	}
	rows, err := billing.DetectAnomalies(h.db, *from, *to, pct)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"anomalies": rows})
}

func spendPeriod(c *gin.Context) (*time.Time, *time.Time, error) {
	var from, to *time.Time
	if s := c.Query("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, nil, errors.New("from must be RFC3339")
		}
		from = &t
	}
	if s := c.Query("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, nil, errors.New("to must be RFC3339")
		}
		to = &t
	}
	return from, to, nil
}

func (h *Handler) actualSpend(kind string, id uint, key, from, to string) int64 {
	q := h.db.Model(&storage.CostRecord{}).Select("COALESCE(SUM(amount_cents),0)")
	if from != "" {
		q = q.Where("incurred_at >= ?", from)
	}
	if to != "" {
		q = q.Where("incurred_at <= ?", to)
	}
	switch kind {
	case billing.TargetProvider:
		if id > 0 {
			q = q.Where("cost_provider_id = ?", id)
		}
	case billing.TargetAccount:
		q = q.Where("account = ?", key)
	case billing.TargetRegion:
		q = q.Where("region = ?", key)
	default:
		return 0
	}
	var sum int64
	_ = q.Scan(&sum).Error
	return sum
}

func (h *Handler) estimatedSpend(kind string, id uint, key, from, to string) int64 {
	q := h.db.Model(&storage.CostEstimate{}).Select("COALESCE(SUM(amount_cents),0)").Where("unknown_price = ?", false)
	if from != "" {
		q = q.Where("period_start >= ?", from)
	}
	if to != "" {
		q = q.Where("period_end <= ?", to)
	}
	switch kind {
	case billing.TargetNode:
		q = q.Where("node_id = ?", id)
	case billing.TargetPath:
		q = q.Where("path_id = ?", id)
	case billing.TargetRegion:
		q = q.Where("region = ?", key)
	default:
		return 0
	}
	var sum int64
	_ = q.Scan(&sum).Error
	return sum
}

// listPathCosts serves GET /api/cost/path-estimates (F-0603): the explainable
// per-path cost estimate with freshness marking (F-0604); stale or unknown
// prices lose the Precise flag instead of inventing amounts (F-0610).
func (h *Handler) listPathCosts(c *gin.Context) {
	fromS := c.Query("from")
	if fromS == "" {
		fail(c, http.StatusBadRequest, errors.New("from is required"))
		return
	}
	from, err := time.Parse(time.RFC3339, fromS)
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("from must be RFC3339"))
		return
	}
	to := from.AddDate(0, 1, 0)
	if s := c.Query("to"); s != "" {
		if to, err = time.Parse(time.RFC3339, s); err != nil {
			fail(c, http.StatusBadRequest, errors.New("to must be RFC3339"))
			return
		}
	}
	rows, err := billing.PathCostEstimates(h.db, from, to, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows})
}
