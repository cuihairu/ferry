package handler

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/cardcode"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// maxCardsPerBatch 单批上限：批量生成走管理员路径，防误传大载荷。
const maxCardsPerBatch = 10000

// cardBatchInput 是建批次的请求载荷（PAY-3，授权面随 P1-1 登录收敛）。
type cardBatchInput struct {
	Name          string     `json:"name"`
	GrantType     string     `json:"grant_type"` // add_quota / extend_days
	GrantValue    int64      `json:"grant_value"`
	PriceCents    int64      `json:"price_cents"` // 在线售价（分），0=仅兑换不出售（PAY-11）
	Total         int        `json:"total"`
	ExpiredAt     *time.Time `json:"expired_at,omitempty"`
	CreatedBy     string     `json:"created_by"`
	DistributorID uint       `json:"distributor_id"` // DS-1：代理归属，0=面板自营
}

// createCardBatch 生成一批卡密：批次与卡密同事务落库，任一失败整批回滚。
func (h *Handler) createCardBatch(c *gin.Context) {
	var in cardBatchInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 128 {
		fail(c, http.StatusBadRequest, errors.New("name is required (1-128 chars)"))
		return
	}
	if in.GrantType != "add_quota" && in.GrantType != "extend_days" {
		fail(c, http.StatusBadRequest, errors.New("grant_type must be add_quota or extend_days"))
		return
	}
	if in.GrantValue <= 0 {
		fail(c, http.StatusBadRequest, errors.New("grant_value must be > 0"))
		return
	}
	if in.GrantType == "extend_days" && in.GrantValue > 3650 {
		fail(c, http.StatusBadRequest, errors.New("extend_days value must be <= 3650"))
		return
	}
	if in.PriceCents < 0 {
		fail(c, http.StatusBadRequest, errors.New("price_cents must be >= 0"))
		return
	}
	if in.Total < 1 || in.Total > maxCardsPerBatch {
		fail(c, http.StatusBadRequest, errors.New("total must be 1-10000"))
		return
	}
	if in.ExpiredAt != nil && !in.ExpiredAt.After(time.Now()) {
		fail(c, http.StatusBadRequest, errors.New("expired_at must be in the future"))
		return
	}
	in.CreatedBy = strings.TrimSpace(in.CreatedBy)
	if in.CreatedBy == "" {
		in.CreatedBy = "admin"
	}
	// DS-1：代理归属须指向真实代理（批次归属建时定，代理不可自改）。
	if in.DistributorID > 0 {
		var cnt int64
		if err := h.db.Model(&storage.Distributor{}).Where("id = ?", in.DistributorID).Count(&cnt).Error; err != nil || cnt == 0 {
			fail(c, http.StatusBadRequest, errors.New("distributor_id not found"))
			return
		}
	}

	codes, err := cardcode.Generate(in.Total)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	batch := storage.CardBatch{
		Name: in.Name, GrantType: in.GrantType, GrantValue: in.GrantValue,
		PriceCents: in.PriceCents, Total: in.Total, ExpiredAt: in.ExpiredAt, CreatedBy: in.CreatedBy,
		DistributorID: in.DistributorID,
	}
	rows := make([]storage.CardCode, 0, len(codes))
	for _, code := range codes {
		rows = append(rows, storage.CardCode{Code: code})
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].BatchID = batch.ID
		}
		return tx.Create(&rows).Error
	}); err != nil {
		if isDup(err) {
			fail(c, http.StatusConflict, errors.New("code collision, retry"))
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"batch": batch, "codes": codes})
}

// cardBatchView 是批次列表行：附剩余可用数。
type cardBatchView struct {
	storage.CardBatch
	Remaining int64 `json:"remaining"`
}

func (h *Handler) listCardBatches(c *gin.Context) {
	var batches []storage.CardBatch
	if err := h.db.Order("id DESC").Find(&batches).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	remaining := map[uint]int64{}
	var rows []struct {
		BatchID uint
		Cnt     int64
	}
	if err := h.db.Model(&storage.CardCode{}).
		Select("batch_id, COUNT(*) AS cnt").Where("status = ?", "unused").
		Group("batch_id").Scan(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	for _, r := range rows {
		remaining[r.BatchID] = r.Cnt
	}
	out := make([]cardBatchView, 0, len(batches))
	for _, b := range batches {
		out = append(out, cardBatchView{CardBatch: b, Remaining: remaining[b.ID]})
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) listCardCodes(c *gin.Context) {
	batch, ok := h.findCardBatch(c)
	if !ok {
		return
	}
	var codes []storage.CardCode
	if err := h.db.Where("batch_id = ?", batch.ID).Order("id").Find(&codes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, codes)
}

// exportCardBatchCSV 导出批次卡密明文（code,status），供站外发放。
func (h *Handler) exportCardBatchCSV(c *gin.Context) {
	batch, ok := h.findCardBatch(c)
	if !ok {
		return
	}
	var codes []storage.CardCode
	if err := h.db.Where("batch_id = ?", batch.ID).Order("id").Find(&codes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="card-batch-`+strconv.FormatUint(uint64(batch.ID), 10)+`.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", cardBatchCSV(batch, codes))
}

func cardBatchCSV(batch storage.CardBatch, codes []storage.CardCode) []byte {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"code", "status"})
	for _, code := range codes {
		_ = w.Write([]string{code.Code, code.Status})
	}
	w.Flush()
	return []byte(buf.String())
}

// disableCardCode 手动禁用一张卡密：仅 unused 可禁（used 保留核销事实，
// disabled 终态不可逆），条件更新原子完成避免与兑换并发竞争。
func (h *Handler) disableCardCode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var existing storage.CardCode
	if err := h.db.First(&existing, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 可解析但不存在的 id 与不可禁用同归 409，不区分「不存在」（防枚举口径）。
			c.JSON(http.StatusConflict, gin.H{"error": "仅未使用的卡密可禁用"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	res := h.db.Model(&storage.CardCode{}).
		Where("id = ? AND status = ?", uint(id), "unused").
		Update("status", "disabled")
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "仅未使用的卡密可禁用"})
		return
	}
	var code storage.CardCode
	if err := h.db.First(&code, uint(id)).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, code)
}

// deleteCardBatch 删除批次：DB 级联连带卡密（《支付设计》§3.1），
// 导出发放前误建批次可整体回收。
func (h *Handler) deleteCardBatch(c *gin.Context) {
	batch, ok := h.findCardBatch(c)
	if !ok {
		return
	}
	if err := h.db.Delete(&batch).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": batch.ID})
}

func (h *Handler) findCardBatch(c *gin.Context) (storage.CardBatch, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return storage.CardBatch{}, false
	}
	var batch storage.CardBatch
	if err := h.db.First(&batch, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return storage.CardBatch{}, false
		}
		fail(c, http.StatusInternalServerError, err)
		return storage.CardBatch{}, false
	}
	return batch, true
}
