package handler

import (
	"errors"
	"net/http"

	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 落地自动分配（E-21）管理端：策略查看/设置与现行 auto 分配行。
// 分配判定由 alloc.Loop 周期执行，这里只读结果与改策略。

// listAlloc 返回当前策略与生效中的 auto 分配行。
func (h *Handler) listAlloc(c *gin.Context) {
	policy, err := alloc.LoadPolicy(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	rows := []storage.LandingAssignment{}
	if err := h.db.Where("released_at IS NULL AND strategy != ?", alloc.PolicyManual).
		Order("id DESC").Limit(500).Find(&rows).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy, "rows": rows})
}

// putAllocPolicy 设置各方向分配档位。
func (h *Handler) putAllocPolicy(c *gin.Context) {
	var in alloc.PolicySetting
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := alloc.SavePolicy(h.db, in); err != nil {
		fail(c, http.StatusBadRequest, errors.New("policy must be least_conn/cost_first/perf_first/balanced"))
		return
	}
	c.JSON(http.StatusOK, in)
}
