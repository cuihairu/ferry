package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/support"
	"github.com/gin-gonic/gin"
)

// Panel support entry point (servify real-embed acceptance): sends the embed config
// plus a guest token to the panel frontend, which loads the widget (widget.js) on demand;
// when support is not wired up (FERRY_SUPPORT_URL empty) it always reports
// enabled=false, so the panel renders no widget and makes no external calls. Ticket
// context goes through server-side sync (read-only for agents), and the service key
// never reaches the browser.

// supportSessionID is the user's stable session identifier on the servify side: the same
// user resumes the same session across pages/devices (embed guide section 3.1).
func supportSessionID(uid uint) string {
	return fmt.Sprintf("ferry_user_%d", uid)
}

// supportEmail is the servify customer dedup key: ferry users have no email field, so a stable synthetic address is used.
func supportEmail(uid uint) string {
	return fmt.Sprintf("u%d@users.ferry.local", uid)
}

// panelSupport returns the panel support embed config (GET /api/panel/support).
// When servify is unreachable it degrades to enabled=false (support is a side channel
// and must not drag the panel down).
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

	// Business snapshot (read-only on the agent side): quota level / expiry / status overwritten once per request.
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
			// Show it on the server's local calendar (same policy as the billing
			// window / toucher), so on a non-UTC deployment support does not see
			// "just expired" while the user side is still valid.
			expiry = u.ExpiresAt.Local().Format("2006-01-02")
		}
		notes = fmt.Sprintf("状态：%s；流量：%d/%d 字节（%d%%）；到期：%s",
			state, used, u.QuotaBytes, pct, expiry)
	}
	snap := support.CustomerSnapshot{
		Email: supportEmail(u.ID),
		Name:  u.Username,
		Notes: notes,
	}
	// A failed context sync must not block the chat: the token is issued anyway, and the sync state is reported back to the panel for troubleshooting.
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
