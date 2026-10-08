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

// 成本参考库（E-31）管理端：价格表导入、单点试查与批量偏差对账。
// 参考价只提示不改价——偏差行供节点卡「手录价偏离牌价」人工复核
// （套餐与成本设计 §4.2）。

// refTolerance 读偏差容忍度参数（百分比），缺省 30，夹在 1..200。
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

// refSource 取已导入的参考价来源；未导入回 nil（调用方按 400 指引导入）。
func (h *Handler) refSource() (costref.Source, error) {
	return cost.LoadRefSource(h.db)
}

// getCostRef 单点试查：?node_id= 按节点映射（机房/区域/套餐档 + 手录价），
// 或 provider/region/spec + manual_cents/currency 自由键。
// 未命中回 200+hit=false（面板表格直显，不算错）；币种不符/手录未填
// 回 reason 字段说明为何没有 deviation。
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

// getCostRefCheck 批量对账：机房+月固定成本齐全的节点逐个比对，回命中
// 且可比的行（含偏差）；未命中/币种不符只进计数。数据源供成本页偏差表。
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
			continue // 未命中不进列表
		}
		if errors.Is(err, costref.ErrCurrencyMismatch) {
			mismatches++
			continue
		}
		if err != nil {
			continue // 其余（理论不可达）不阻塞整表
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

// putCostRefTable 导入/清空价格表：table 为 JSON 数组文本（导入层校验，
// 拒脏表）；空串清空停用。
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
	// 回读条数给个导入确认。
	var rows []costref.Entry
	_ = json.Unmarshal([]byte(in.Table), &rows)
	c.JSON(http.StatusOK, gin.H{"saved": true, "rows": len(rows)})
}
