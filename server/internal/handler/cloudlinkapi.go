package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudlink"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// cloudlinkAPI exposes the cloud resource ↔ Node linkage controls (F-0391~
// F-0404): controlled status refresh, pre-shutdown traffic risk check,
// maintenance drain, post-boot Agent health check and path scheduling health.
// All routes live behind the apiAuth+auditMiddleware admin plane.

func (h *Handler) cloudlinkService() *cloudlink.Service {
	return cloudlink.New(h.db, h.secrets, nil)
}

// linkCloudResource refreshes one bound resource from the vendor and applies
// the mapped Node state (F-0398). The vendor query is authoritative; no Node
// state is changed on a vendor outage.
func (h *Handler) linkCloudResource(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("cloud resource not found"))
		return
	}
	var res struct {
		ProviderID uint `json:"provider_id"`
	}
	if err := h.db.Model(&storage.CloudResource{}).Select("provider_id").First(&res, uint(id)).Error; err != nil {
		fail(c, http.StatusNotFound, errors.New("cloud resource not found"))
		return
	}
	eff, changed, err := h.cloudlinkService().RefreshAndApply(c.Request.Context(), res.ProviderID, uint(id), time.Now())
	if errors.Is(err, cloudlink.ErrNoNode) {
		fail(c, http.StatusConflict, err)
		return
	}
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"resource_id": id, "effect": eff, "changed": changed,
	})
}

// preflightCloudShutdown reports user traffic on the Node before a shutdown
// operation is submitted (F-0400); the client must show the risk warning.
func (h *Handler) preflightCloudShutdown(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("node not found"))
		return
	}
	out, err := h.cloudlinkService().CheckShutdownRisk(uint(id), time.Now(), 0)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id": out.NodeID, "risk": out.Risk, "recent_conns": out.RecentConns, "message": out.Message,
	})
}

// drainCloudNode starts a maintenance window (F-0401): the Node stays online
// but leaves subscriptions and allocation before a shutdown op runs.
func (h *Handler) drainCloudNode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("node not found"))
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&in)
	if err := h.cloudlinkService().Drain(uint(id), in.Reason, time.Now()); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// checkCloudNodeBoot verifies the Agent after a boot (F-0392/F-0393/F-0394):
// no cloud state is trusted, and a failed check emits one alert (F-0403).
func (h *Handler) checkCloudNodeBoot(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("node not found"))
		return
	}
	health, err := h.cloudlinkService().AfterBoot(uint(id), time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"agent_online": health.AgentOnline, "heartbeat_fresh": health.HeartbeatFresh,
		"config_valid": health.ConfigValid, "crash_alert": health.CrashAlert, "healthy": health.Healthy,
	})
}

// cloudNodePathHealth reports the scheduling health of paths touching the Node
// (F-0399), the same verdict the subscription and dash surfaces use.
func (h *Handler) cloudNodePathHealth(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, http.StatusNotFound, errors.New("node not found"))
		return
	}
	states, err := h.cloudlinkService().PathHealth(uint(id), time.Now())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"node_id": id, "paths": states})
}
