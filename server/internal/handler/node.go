package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/cuihairu/ferry/server/internal/model"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handler) listNodes(c *gin.Context) {
	out := []storage.Node{}
	if err := h.db.Order("id").Find(&out).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) getNode(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	c.JSON(http.StatusOK, n)
}

func (h *Handler) createNode(c *gin.Context) {
	var in model.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := validateNodeInput(&in, true); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	token, err := randomToken()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	n := storage.Node{
		Name:     strings.TrimSpace(in.Name),
		Address:  strings.TrimSpace(in.Address),
		Port:     in.Port,
		Protocol: in.Protocol,
		Config:   normalizeConfig(in.Config),
		Enabled:  enabled,
		Token:    token,
		Status:   "unknown",
	}
	// 元数据：非空覆盖库默认值，任一出现即 MetaInit（面板接管，注册不再覆盖）。
	applyMetaInput(&n, &in)
	if err := h.db.Create(&n).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !enabled {
		// Enabled 带 default:true，零值 false 会被 GORM 略过落库默认值，需显式补写。
		if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).Update("enabled", false).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		n.Enabled = false
	}
	c.JSON(http.StatusCreated, n)
}

func (h *Handler) updateNode(c *gin.Context) {
	n, err := h.findNode(c.Param("id"))
	if err != nil {
		replyFind(c, err)
		return
	}
	var in model.NodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	// 未提供的字段保持原值，整体校验后回写。
	if in.Name == "" {
		in.Name = n.Name
	}
	if in.Address == "" {
		in.Address = n.Address
	}
	if in.Port == 0 {
		in.Port = n.Port
	}
	if in.Protocol == "" {
		in.Protocol = n.Protocol
	}
	if in.Config == "" {
		in.Config = n.Config
	}
	if err := validateNodeInput(&in, false); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	enabled := n.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	updates := map[string]any{
		"name":     strings.TrimSpace(in.Name),
		"address":  strings.TrimSpace(in.Address),
		"port":     in.Port,
		"protocol": in.Protocol,
		"config":   normalizeConfig(in.Config),
		"enabled":  enabled,
	}
	// 元数据未提供保持原值（与基础字段同口径），提供则校验后回写。
	metaProvided := false
	for k, v := range metaUpdates(&in) {
		updates[k] = v
		metaProvided = true
	}
	if metaProvided {
		// 面板改过元数据后，注册上报不再覆盖。
		updates["meta_init"] = true
	}
	if err := h.db.Model(&storage.Node{}).Where("id=?", n.ID).Updates(updates).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	updated, err := h.findNode(strconv.FormatUint(uint64(n.ID), 10))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *Handler) deleteNode(c *gin.Context) {
	res := h.db.Where("id=?", c.Param("id")).Delete(&storage.Node{})
	if res.Error != nil {
		fail(c, http.StatusInternalServerError, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// findNode 按主键查节点，不存在返回 errNotFound（非法 id 同样视为不存在）。
func (h *Handler) findNode(id string) (*storage.Node, error) {
	uid, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return nil, errNotFound
	}
	var n storage.Node
	if err := h.db.First(&n, uint(uid)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &n, nil
}

// normalizeConfig 保证模板存的是紧凑 JSON 文本；空对象输入回退为 {}。
func normalizeConfig(cfg string) string {
	trimmed := strings.TrimSpace(cfg)
	if trimmed == "" {
		return "{}"
	}
	var v any
	if json.Unmarshal([]byte(trimmed), &v) != nil {
		return trimmed // validateNodeInput 已确保合法，这里仅防御
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return trimmed
	}
	return string(compact)
}

// metaUpdates 把 NodeInput 里非空的元数据字段转成列更新；空串表示保持原值。
func metaUpdates(in *model.NodeInput) map[string]any {
	out := map[string]any{}
	if in.Role != "" {
		out["role"] = in.Role
	}
	if in.Direction != "" {
		out["direction"] = in.Direction
	}
	if in.LineType != "" {
		out["line_type"] = in.LineType
	}
	if in.Region != "" {
		out["region"] = in.Region
	}
	if in.City != "" {
		out["city"] = in.City
	}
	if in.Datacenter != "" {
		out["datacenter"] = in.Datacenter
	}
	if in.ISP != "" {
		out["isp"] = in.ISP
	}
	if in.Transport != "" {
		out["transport"] = in.Transport
	}
	return out
}

// applyMetaInput 建节点时按请求落元数据；任一元数据出现即视为面板接管。
func applyMetaInput(n *storage.Node, in *model.NodeInput) {
	provided := false
	set := func(dst *string, v string) {
		if v != "" {
			*dst = strings.TrimSpace(v)
			provided = true
		}
	}
	set(&n.Role, in.Role)
	set(&n.Direction, in.Direction)
	set(&n.LineType, in.LineType)
	set(&n.Region, in.Region)
	set(&n.City, in.City)
	set(&n.Datacenter, in.Datacenter)
	set(&n.ISP, in.ISP)
	set(&n.Transport, in.Transport)
	if provided {
		n.MetaInit = true
	}
}

func validateNodeInput(in *model.NodeInput, creating bool) error {
	if creating && strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if creating && strings.TrimSpace(in.Address) == "" {
		return errors.New("address is required")
	}
	if in.Port < 1 || in.Port > 65535 {
		return errors.New("port must be 1-65535")
	}
	if !model.ValidProtocol(in.Protocol) {
		return errors.New("protocol must be one of vless/vmess/trojan/shadowsocks")
	}
	if strings.TrimSpace(in.Config) != "" && !json.Valid([]byte(in.Config)) {
		return errors.New("config must be valid JSON")
	}
	for _, v := range []string{in.Role} {
		switch v {
		case "", "entry", "landing", "both":
		default:
			return errors.New("role must be entry/landing/both")
		}
	}
	switch in.Direction {
	case "", "out", "in", "both":
	default:
		return errors.New("direction must be out/in/both")
	}
	switch in.Transport {
	case "", "tls", "quic", "ws-tls", "ssh":
	default:
		return errors.New("transport must be tls/quic/ws-tls/ssh")
	}
	return nil
}

// fail 输出统一错误结构；内部错误只记日志不外泄细节。
func fail(c *gin.Context, status int, err error) {
	if status >= 500 {
		c.Error(err) // gin 日志记录
		c.JSON(status, gin.H{"error": "internal error"})
		return
	}
	c.JSON(status, gin.H{"error": err.Error()})
}

var errNotFound = errors.New("not found")

func replyFind(c *gin.Context, err error) {
	if errors.Is(err, errNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	fail(c, http.StatusInternalServerError, err)
}
