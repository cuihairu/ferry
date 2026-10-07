package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/agenthub"
	"github.com/cuihairu/ferry/server/internal/routing"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// rulelibAckTimeout 与 config.push 同口径：覆盖 agent 侧落盘 + reload 最坏耗时。
const rulelibAckTimeout = 60 * time.Second

// rulelibNameRe 限制规则库文件名：防路径穿越，只允许单个文件名。
var (
	rulelibNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	rulelibTraversal = regexp.MustCompile(`\.\.`)
)

// adsSettingKey 是广告拦截开关的设置键（SAVE-4）：缺省开，"0"/"false" 关。
const adsSettingKey = "routing_ads_enabled"

// adsEnabled 读广告拦截开关；未设置按开（设计默认用开源名单）。
func adsEnabled(db *gorm.DB) bool {
	v, ok, err := storage.GetSetting(db, adsSettingKey)
	if err != nil || !ok {
		return true
	}
	return v != "0" && v != "false"
}

// routingSets 按开关组装渲染用清单：广告拦截关时滤掉 AdBlock。
func (h *Handler) routingSets() []routing.RuleSet {
	sets := routing.Sets()
	if !adsEnabled(h.db) {
		return routing.WithoutAds(sets)
	}
	return sets
}

// renderRoutingConfig 返回节点配置模板合成分流规则段后的完整配置（SAVE-1）。
// 只做渲染不落库：下发走既有 POST /api/nodes/:id/config。
func (h *Handler) renderRoutingConfig(c *gin.Context) {
	var n storage.Node
	if err := h.db.First(&n, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	sets := h.routingSets()
	merged, err := routing.Merge(n.Config, sets)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	sum := sha256.Sum256([]byte(merged))
	c.JSON(http.StatusOK, gin.H{
		"node_id": n.ID,
		"sha256":  hex.EncodeToString(sum[:]),
		"config":  merged,
		"sets":    sets,
	})
}

// putAdsEnabled 广告/追踪拦截开关（PUT /api/routing/ads {enabled}）：
// 关掉后渲染的配置不再含屏蔽规则段，已下发配置不动（重渲染重发才生效）。
func (h *Handler) putAdsEnabled(c *gin.Context) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Enabled == nil {
		fail(c, http.StatusBadRequest, errors.New("enabled is required"))
		return
	}
	v := "0"
	if *in.Enabled {
		v = "1"
	}
	if err := storage.SetSetting(h.db, adsSettingKey, v); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": *in.Enabled})
}

// pushRuleLib 向节点分发一份规则库数据文件（geoip.dat/geosite.dat 等）：
// 原始请求体即文件内容，经 config.push 通道（kind=rulelib:<name>）落盘到
// agent 资产目录并触发 reload。每次推送在 node_configs 留版本快照。
func (h *Handler) pushRuleLib(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	proc := c.Query("proc")
	name := c.Query("name")
	if proc == "" {
		fail(c, http.StatusBadRequest, errors.New("proc is required"))
		return
	}
	if !rulelibNameRe.MatchString(name) || rulelibTraversal.MatchString(name) {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid rulelib name %q", name))
		return
	}
	var n storage.Node
	if err := h.db.First(&n, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	payload, err := c.GetRawData()
	if err != nil || len(payload) == 0 {
		fail(c, http.StatusBadRequest, errors.New("request body is required"))
		return
	}

	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	kind := "rulelib:" + name
	row := storage.NodeConfig{
		NodeID: uint(id), Proc: proc, Kind: kind,
		Version: digest, Sha256: digest, Payload: string(payload),
		Status: "pending",
	}
	if err := h.db.Create(&row).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("rulelib-%d-%d", id, time.Now().UnixNano()),
		agentproto.MsgConfigPush,
		agentproto.ConfigPush{Proc: proc, Kind: kind, Version: digest, Sha256: digest, Payload: string(payload)},
	)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	reply, err := h.hub.Request(int64(id), env, rulelibAckTimeout)
	if err != nil {
		h.finishNodeConfig(&row, "failed", false, false, err.Error())
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

	var ack agentproto.ConfigAck
	if err := reply.Decode(&ack); err != nil {
		h.finishNodeConfig(&row, "failed", false, false, "bad config_ack: "+err.Error())
		fail(c, http.StatusBadGateway, err)
		return
	}
	status := "failed"
	if ack.OK {
		status = "applied"
	}
	h.finishNodeConfig(&row, status, ack.Reverted, ack.Validated, ack.Error)
	row.Payload = "" // 版本史不回带文件内容
	c.JSON(http.StatusOK, row)
}

// listRuleLib 返回节点的规则库版本史（node_configs 快照，不含文件内容）。
func (h *Handler) listRuleLib(c *gin.Context) {
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
	if err := h.db.
		Select("id", "node_id", "proc", "kind", "version", "sha256", "status", "reverted", "validated", "error", "created_at", "updated_at").
		Where("node_id=? AND kind LIKE 'rulelib:%'", id).
		Order("id DESC").Limit(limit).Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
