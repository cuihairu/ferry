package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// configAckTimeout 覆盖 agent 侧校验命令 + reload 的最坏耗时。
const configAckTimeout = 60 * time.Second

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

// deployConfig 执行单节点配置下发全流程，返回最终留痕记录。
// 返回 err != nil 时 status 为对应 HTTP 状态码（transport 类失败）；
// agent ack 的 OK=false 属业务失败，记录留痕后按 nil err 返回。
func (h *Handler) deployConfig(nodeID uint, in configPushInput) (storage.NodeConfig, int, error) {
	sum := sha256.Sum256([]byte(in.Payload))
	digest := hex.EncodeToString(sum[:])
	row := storage.NodeConfig{
		NodeID: nodeID, Proc: in.Proc, Kind: in.Kind,
		Version: digest, Sha256: digest, Payload: in.Payload,
		Status: "pending",
	}
	if err := h.db.Create(&row).Error; err != nil {
		return row, http.StatusInternalServerError, err
	}

	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("cfg-%d-%d", nodeID, time.Now().UnixNano()),
		agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: in.Proc, Kind: in.Kind, Version: digest, Sha256: digest, Payload: in.Payload},
	)
	if err != nil {
		return row, http.StatusInternalServerError, err
	}
	reply, err := h.hub.Request(int64(nodeID), env, configAckTimeout)
	if err != nil {
		h.finishNodeConfig(&row, "failed", false, false, err.Error())
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			return row, http.StatusBadGateway, err
		case errors.Is(err, agenthub.ErrTimeout):
			return row, http.StatusGatewayTimeout, err
		default:
			return row, http.StatusInternalServerError, err
		}
	}

	var ack agentproto.ConfigAck
	if err := reply.Decode(&ack); err != nil {
		h.finishNodeConfig(&row, "failed", false, false, "bad config_ack: "+err.Error())
		return row, http.StatusBadGateway, err
	}
	status := "failed"
	if ack.OK {
		status = "applied"
	}
	h.finishNodeConfig(&row, status, ack.Reverted, ack.Validated, ack.Error)
	return row, 0, nil
}

// finishNodeConfig 把下发的最终结果写回记录；失败仅记日志（记录本身已可追溯）。
func (h *Handler) finishNodeConfig(row *storage.NodeConfig, status string, reverted, validated bool, errMsg string) {
	updates := map[string]any{
		"status": status, "reverted": reverted, "validated": validated, "error": errMsg,
	}
	if err := h.db.Model(&storage.NodeConfig{}).Where("id=?", row.ID).Updates(updates).Error; err != nil {
		log.Printf("update node_config id=%d: %v", row.ID, err)
		return
	}
	row.Status, row.Reverted, row.Validated, row.Error = status, reverted, validated, errMsg
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
