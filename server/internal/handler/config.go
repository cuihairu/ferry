package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// configPushInput 是一份配置下发的载荷（单点与批量共用，A-15）。
type configPushInput struct {
	Proc    string `json:"proc"`
	Kind    string `json:"kind"`
	Payload string `json:"payload"`
}

// pushConfig 向节点下发一份进程配置（A-18）：
// 落库 pending → 经 agenthub 等待 config.ack → 按结果置 applied/failed。
func (h *Handler) pushConfig(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	var in configPushInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Proc == "" || in.Payload == "" {
		fail(c, http.StatusBadRequest, errors.New("proc and payload are required"))
		return
	}
	row, status, err := h.deployConfig(uint(id), in)
	if err != nil {
		fail(c, status, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// deployConfig 执行单节点配置下发全流程，委托 relaypush.Pusher 的
// 同链路实现（E-16b：自动换线重指与手动下发共用一条配置推送通道）。
// 返回 err != nil 时 status 为对应 HTTP 状态码（transport 类失败）；
// agent ack 的 OK=false 属业务失败，记录留痕后按 nil err 返回。
func (h *Handler) deployConfig(nodeID uint, in configPushInput) (storage.NodeConfig, int, error) {
	return h.pusher.Push(nodeID, in.Proc, in.Kind, in.Payload)
}

// finishNodeConfig 把下发的最终结果写回记录；委托 pusher 同链路实现。
func (h *Handler) finishNodeConfig(row *storage.NodeConfig, status string, reverted, validated bool, errMsg string) {
	h.pusher.Finish(row, status, reverted, validated, errMsg)
}

// listNodeTraffic 返回节点的流量记账（新在前，默认 200 条）。
func (h *Handler) listNodeTraffic(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	limit := 200
	if v := c.Query("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 1000 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-1000"))
			return
		}
		limit = n
	}
	out := []storage.NodeTrafficLog{}
	if err := h.db.Where("node_id=?", id).Order("recorded_at DESC, id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// listNodeConfigs 返回节点的配置下发历史（新在前，默认 50 条）。
func (h *Handler) listNodeConfigs(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 200 {
			fail(c, http.StatusBadRequest, errors.New("limit must be 1-200"))
			return
		}
		limit = n
	}
	out := []storage.NodeConfig{}
	if err := h.db.Where("node_id=?", id).Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
