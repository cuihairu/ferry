package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Cost reference library (E-31) admin side: price table import, single-point probe and batch deviation reconciliation.
// Reference prices only hint and never change prices — deviation rows feed the node card's "manual price deviates from list price" for human review
// (plan and cost design §4.2).

// refTolerance reads the deviation tolerance parameter (percentage), default 30, clamped to 1..200.
func refTolerance(c *gin.Context) int64 {
	tol := int64(cost.DefaultRefTolerancePct)
	if v := c.Query("tolerance_pct"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			tol = n
		}
	}
	if tol < 1 {
		tol = 1
	}
	if tol > 200 {
		tol = 200
	}
	return tol
}

// refSource gets the imported reference-price source; returns nil when not imported (callers guide import via 400).
func (h *Handler) refSource() (costref.Source, error) {
	return cost.LoadRefSource(h.db)
}

// getCostRef single-point probe: ?node_id= maps by node (datacenter/region/spec tier + manual price),
// or provider/region/spec + manual_cents/currency as free-form keys.
// A miss returns 200+hit=false (the panel table shows it directly, not an error); a currency
// mismatch / missing manual price returns a reason field explaining why there is no deviation.
func (h *Handler) getCostRef(c *gin.Context) {
	tol := refTolerance(c)
	src, err := h.refSource()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if src == nil {
		fail(c, http.StatusBadRequest, errors.New("reference price table not imported yet (PUT /api/cost/ref-table)"))
		return
	}

	q := costref.Query{}
	manual := costref.Manual{Currency: c.Query("currency")}
	if nid := c.Query("node_id"); nid != "" {
		id, err := strconv.ParseUint(nid, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, errors.New("node_id must be a number"))
			return
		}
		var n storage.Node
		if err := h.db.First(&n, id).Error; err != nil {
			fail(c, http.StatusNotFound, errors.New("node not found"))
			return
		}
		q, err = cost.NodeQuery(n)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		manual = costref.Manual{MonthlyCents: n.MonthlyCostCents, Currency: n.Currency}
	} else {
		q.Provider = c.Query("provider")
		q.Region = c.Query("region")
		q.Spec = c.Query("spec")
		if strings.TrimSpace(q.Provider) == "" || strings.TrimSpace(q.Spec) == "" {
			fail(c, http.StatusBadRequest, errors.New("node_id or provider+spec are required"))
			return
		}
		if v := c.Query("manual_cents"); v != "" {
			manual.MonthlyCents, _ = strconv.ParseInt(v, 10, 64)
		}
		if manual.MonthlyCents > 0 && manual.Currency == "" {
			fail(c, http.StatusBadRequest, errors.New("currency is required to compare manual price"))
			return
		}
	}

	out := gin.H{"source": src.Name(), "query": q}
	quote, err := src.Lookup(c.Request.Context(), q)
	if errors.Is(err, costref.ErrNotFound) {
		out["hit"] = false
		c.JSON(http.StatusOK, out)
		return
	}
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	out["hit"] = true
	out["quote"] = quote
	dev, cerr := costref.Compare(manual, quote, tol)
	switch {
	case cerr == nil:
		out["deviation"] = dev
	case errors.Is(cerr, costref.ErrNoManual):
		out["reason"] = "manual monthly cost not set"
	case errors.Is(cerr, costref.ErrCurrencyMismatch):
		out["reason"] = "currency mismatch (manual " + manual.Currency + " vs quote " + quote.Currency + ")"
	default:
		fail(c, http.StatusInternalServerError, cerr)
		return
	}
	c.JSON(http.StatusOK, out)
}

// getCostRefCheck batch reconciliation: nodes with both datacenter and monthly fixed cost are compared
// one by one, returning hit and comparable rows (with deviation); misses/currency mismatches only count. The data source feeds the cost page's deviation table.
func (h *Handler) getCostRefCheck(c *gin.Context) {
	tol := refTolerance(c)
	src, err := h.refSource()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if src == nil {
		fail(c, http.StatusBadRequest, errors.New("reference price table not imported yet (PUT /api/cost/ref-table)"))
		return
	}

	var nodes []storage.Node
	if err := h.db.Where("datacenter != '' AND monthly_cost_cents > 0").Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	type refItem struct {
		NodeID      uint              `json:"node_id"`
		NodeName    string            `json:"node_name"`
		Query       costref.Query     `json:"query"`
		Quote       costref.Quote     `json:"quote"`
		ManualCents int64             `json:"manual_cents"`
		Currency    string            `json:"currency"`
		Deviation   costref.Deviation `json:"deviation"`
	}
	items := []refItem{}
	mismatches := 0
	for _, n := range nodes {
		quote, dev, err := cost.CheckNode(src, n, tol)
		if errors.Is(err, costref.ErrNotFound) {
			continue // misses are not listed
		}
		if errors.Is(err, costref.ErrCurrencyMismatch) {
			mismatches++
			continue
		}
		if err != nil {
			continue // the rest (theoretically unreachable) do not block the whole table
		}
		items = append(items, refItem{NodeID: n.ID, NodeName: n.Name, Query: costref.Query{
			Provider: n.Datacenter, Region: n.Region, Spec: cost.SpecOf(n.BwDownMbps, n.MonthlyTrafficQuotaBytes),
		}, Quote: quote, ManualCents: n.MonthlyCostCents, Currency: n.Currency, Deviation: dev})
	}
	c.JSON(http.StatusOK, gin.H{
		"source": src.Name(), "tolerance_pct": tol, "checked": len(nodes),
		"hits": len(items), "mismatches": mismatches, "items": items,
	})
}

// putCostRefTable imports/clears the price table: table is JSON array text (validated at the
// import layer, dirty tables rejected); an empty string clears and disables it.
func (h *Handler) putCostRefTable(c *gin.Context) {
	var in struct {
		Table string `json:"table"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := cost.SaveRefTable(h.db, []byte(in.Table)); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Table) == "" {
		c.JSON(http.StatusOK, gin.H{"cleared": true})
		return
	}
	// Read back the row count as import confirmation.
	var rows []costref.Entry
	_ = json.Unmarshal([]byte(in.Table), &rows)
	c.JSON(http.StatusOK, gin.H{"saved": true, "rows": len(rows)})
}
