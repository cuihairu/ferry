package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 入口池查看与手动复位（E-16）：摘挂判定在 internal/pool 周期执行，
// 这里给 dash 池视角与探测源全挂时的人工摘回。

// poolView 是入口池一行：节点本体 + 窗口内最新探测结论概要。
type poolView struct {
	storage.Node
	LastVerdict string     `json:"last_verdict"` // healthy/sick，空=窗口内无结论
	LastProbed  *time.Time `json:"last_probed"`
}

// listPool 入口池列表（GET /api/pool）：entry/both 且 enabled 的节点。
func (h *Handler) listPool(c *gin.Context) {
	var nodes []storage.Node
	if err := h.db.Where("enabled = ? AND role IN (?, ?)", true, "entry", "both").
		Order("id").Find(&nodes).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	since := time.Now().Add(-pool.ProbeFresh)
	out := make([]poolView, 0, len(nodes))
	for _, n := range nodes {
		v := poolView{Node: n}
		var r storage.ProbeReport
		err := h.db.Where("target_node_id = ? AND probed_at > ?", n.ID, since).
			Order("probed_at DESC, id DESC").First(&r).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			// 窗口内无结论，留空
		case err != nil:
			fail(c, http.StatusInternalServerError, err)
			return
		default:
			v.LastVerdict = r.Verdict
			v.LastProbed = &r.ProbedAt
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, out)
}

// suspendPoolNode 手动摘除（POST /api/pool/:id/suspend）：立即出池留痕，
// 订阅入口池即时消失；重复摘除 409，非入口节点 400。
func (h *Handler) suspendPoolNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	node, err := pool.Suspend(h.db, uint(id), nil)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, node)
	case errors.Is(err, pool.ErrAlreadySuspended):
		c.JSON(http.StatusConflict, gin.H{"error": "节点已在摘除状态"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		fail(c, http.StatusBadRequest, err)
	}
}

// resumePoolNode 手动复位（POST /api/pool/:id/resume）：仅摘除态可复位，
// 其余口径（404/409）与防枚举一致。
func (h *Handler) resumePoolNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	node, err := pool.Resume(h.db, uint(id), nil)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, node)
	case errors.Is(err, pool.ErrNotSuspended):
		c.JSON(http.StatusConflict, gin.H{"error": "节点不在摘除状态"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	default:
		fail(c, http.StatusInternalServerError, err)
	}
}
