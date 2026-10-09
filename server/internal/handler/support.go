package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/support"
	"github.com/gin-gonic/gin"
)

// 面板客服入口（servify 真嵌验收）：把嵌入配置 + 访客 token 发给面板前端，
// 组件直嵌（widget.js）由前端按需加载；未接客服（FERRY_SUPPORT_URL 空）时
// 恒报 enabled=false，面板零组件零外呼。工单上下文走服务端同步（坐席只读），
// service key 不进浏览器。

// supportSessionID 是用户在 servify 侧的稳定会话标识：同用户跨页面/设备
// 恢复同一会话（嵌入指南 §3.1）。
func supportSessionID(uid uint) string {
	return fmt.Sprintf("ferry_user_%d", uid)
}

// supportEmail 是 servify 客户去重键：ferry 用户无邮箱字段，用稳定合成地址。
func supportEmail(uid uint) string {
	return fmt.Sprintf("u%d@users.ferry.local", uid)
}

// panelSupport 返回面板客服嵌入配置（GET /api/panel/support）。
// servify 不可达时降级为 enabled=false（客服是旁路，不拖垮面板）。
func (h *Handler) panelSupport(c *gin.Context) {
	u, ok := h.panelUser(c)
	if !ok {
		return
	}
	url := h.cfg.SupportURL
	key := h.cfg.SupportServiceKey
	if url == "" || key == "" {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}

	cl := &support.Client{BaseURL: url, ServiceKey: key}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()

	// 业务快照（坐席侧只读）：配额水位/到期/状态一次一覆盖。
	var usage trafficUsage
	notes := ""
	if err := h.usageQuery(u.ID, u.ResetCycle, time.Now()).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Scan(&usage).Error; err == nil {
		used := usage.Rx + usage.Tx
		pct := int64(0)
		if u.QuotaBytes > 0 {
			pct = used * 100 / u.QuotaBytes
		}
		state := "启用"
		if !u.Enabled {
			state = "停用"
		}
		expiry := "长期有效"
		if u.ExpiresAt != nil {
			expiry = u.ExpiresAt.Format("2006-01-02")
		}
		notes = fmt.Sprintf("状态：%s；流量：%d/%d 字节（%d%%）；到期：%s",
			state, used, u.QuotaBytes, pct, expiry)
	}
	snap := support.CustomerSnapshot{
		Email: supportEmail(u.ID),
		Name:  u.Username,
		Notes: notes,
	}
	// 上下文同步失败不挡聊天：token 照发，同步态回给面板供排查。
	contextSynced := cl.SyncCustomer(ctx, snap) == nil

	token, expiresAt, err := cl.GuestToken(ctx, supportSessionID(u.ID))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":        true,
		"url":            url,
		"session_id":     supportSessionID(u.ID),
		"access_token":   token,
		"expires_at":     expiresAt,
		"context_synced": contextSynced,
		"icon":           "headset",
		"color":          "#6e79d6",
		"theme":          "auto",
		"brand": gin.H{
			"name":    "ferry 支持",
			"welcome": "您好，遇到节点或流量问题可以直接留言，会转人工处理。",
		},
	})
}
