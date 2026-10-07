package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/gin-gonic/gin"
)

// 成本看板（E-23）：月度成本核算数据与高成本告警阈值设置。
// 告警检查由 cost.Loop 周期执行，这里读报告与改阈值。

// getCost 返回成本看板数据（月至今流量花费/固定成本/预估+维度汇总+阈值）。
func (h *Handler) getCost(c *gin.Context) {
	rep, err := cost.Build(h.db, time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rep)
}

// putCostThreshold 设置高成本告警阈值（分/月）。
func (h *Handler) putCostThreshold(c *gin.Context) {
	var in struct {
		Cents *int64 `json:"cents"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Cents == nil {
		fail(c, http.StatusBadRequest, errors.New("cents is required"))
		return
	}
	if err := cost.SaveThreshold(h.db, *in.Cents); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cents": *in.Cents})
}
