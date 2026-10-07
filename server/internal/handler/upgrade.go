package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/gin-gonic/gin"
)

// upgradeAckTimeout 覆盖 agent 侧受理判断（下载在后台进行，不在此等待）。
const upgradeAckTimeout = 15 * time.Second

// nodeUpgrade 下发自升级指令（POST /api/nodes/:id/upgrade {version, url, sha256?}）：
// agent 即时应答受理/拒绝，实际升级结果经 hello 版本变化体现。
func (h *Handler) nodeUpgrade(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in struct {
		Version string `json:"version"`
		URL     string `json:"url"`
		Sha256  string `json:"sha256"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.Version == "" || in.URL == "" {
		fail(c, http.StatusBadRequest, errors.New("version and url are required"))
		return
	}
	if _, err := url.ParseRequestURI(in.URL); err != nil {
		fail(c, http.StatusBadRequest, errors.New("url must be absolute"))
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("upg-%d-%d", n.ID, time.Now().UnixNano()),
		agentproto.MsgUpgrade,
		agentproto.Upgrade{Version: in.Version, URL: in.URL, Sha256: in.Sha256},
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(n.ID), env, upgradeAckTimeout)
	if err != nil {
		switch {
		case errors.Is(err, agenthub.ErrOffline):
			fail(c, http.StatusBadGateway, err)
		case errors.Is(err, agenthub.ErrTimeout):
			fail(c, http.StatusGatewayTimeout, err)
		default:
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	var ack agentproto.UpgradeAck
	if err := reply.Decode(&ack); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	if !ack.OK {
		fail(c, http.StatusBadRequest, errors.New("agent rejected: "+ack.Error))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id": n.ID, "version": ack.Version, "accepted": true,
		"current_version": n.AgentVersion,
	})
}
