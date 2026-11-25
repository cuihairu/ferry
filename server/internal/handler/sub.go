package handler

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// trafficUsage aggregates a user's cumulative usage.
type trafficUsage struct {
	Rx int64
	Tx int64
}

// subTargetByUA negotiates the subscription format by client UA (when ?target= is not
// explicitly given): clash family (clash/clash-verge/mihomo) gets YAML; sing-box native
// and Stash get outbounds JSON; everything else falls back to v2ray base64 (SHOP-2).
// Shared by the main subscription and the custom subscriptions.
func subTargetByUA(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "clash"):
		return "clash"
	case strings.Contains(ua, "sing-box"), strings.Contains(ua, "stash"):
		return "singbox"
	default:
		return "v2ray"
	}
}

// subscription handles GET /sub/:token (P0-6/P0-7/P0-8/E-19):
// rate limited per token (Security Design §4, anti-enumeration; 429 hits are logged with
// the token redacted to its prefix); disabled users are treated as 404 (tokens count as
// revoked, anti-enumeration); expired/over-quota users get an empty subscription plus
// usage headers; the entry list is grouped by region with aggregated probe RTT in the
// remarks. target=v2ray|clash|singbox can be given explicitly (singbox = a sing-box
// outbounds array, hy2 batch); by default it is detected from the UA (clash family gets
// clash, sing-box/Stash get singbox, everything else v2ray, SHOP-2).
func (h *Handler) subscription(c *gin.Context) {
	token, ip := c.Param("token"), c.ClientIP()
	if !h.subMissLimiter.Allow(ip) || !h.subLimiter.Allow(token) {
		log.Printf("sub rate limited: token=%s… ip=%s", token[:min(6, len(token))], ip)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
		return
	}
	var u storage.User
	if err := h.db.Where("sub_token = ?", token).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.subMissLimiter.RecordFailure(ip)
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !u.Enabled {
		h.subMissLimiter.RecordFailure(ip)
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	// Device/IP count checks (P2-4): the IP facet always runs (client-independent);
	// the hwid facet only applies when the client sends X-Client-Hwid -- without it the
	// device limit does not affect that request (client cooperation is out of our
	// control; over-limit risk noted as todo). Already-registered devices/IPs are
	// unaffected.
	if err := h.subDeviceCheck(c, &u, c.GetHeader("X-Client-Hwid"), ip, c.Request.UserAgent()); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	var usage trafficUsage
	if err := h.usageQuery(u.ID, u.ResetCycle, time.Now()).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Scan(&usage).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	entries := []sub.Entry{}
	if sub.UserActive(&u, usage.Rx+usage.Tx, time.Now()) {
		// Entry list delivered by the subscription (E-19): only entry/both-role nodes not
		// yet removed (E-16 pool state active); landing nodes are not delivered.
		// Nodes still provisioning (OS-3) stay out of the subscription until they turn
		// online, at which point they enter the list automatically.
		var nodes []storage.Node
		if err := h.db.Where("enabled = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
			true, "entry", "both", pool.StateActive, "provisioning").
			Order("id").Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		rtt := h.entryRttMs()
		entries = make([]sub.Entry, 0, len(nodes))
		for _, n := range nodes {
			entries = append(entries, sub.Entry{Node: n, RttMs: rtt[n.ID]})
		}
		// Quota-linked downgrade (SAVE-6): active linkage rows filter by their snapshot
		// tier line -- only low-cost-tier entries are served, so new connections naturally
		// land on low-cost landing nodes; with no low-cost tier available the subscription
		// is empty (same semantics as the over-quota empty subscription; users are informed
		// via in-panel mail).
		action, err := quota.ActiveAction(h.db, u.ID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		if action != nil {
			tier := quota.LinkSetting{MaxPriceCents: action.MaxPriceCents}
			filtered := entries[:0]
			for _, e := range entries {
				if tier.LowCost(e.Node) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
		}
	}

	target := c.Query("target")
	if target == "" {
		target = subTargetByUA(c.Request.UserAgent())
	}

	// P0-7 headers: usage and expiry (rx as seen by the node counts as user upload, tx as download).
	c.Header("Subscription-Userinfo", userinfoHeader(u, usage))
	c.Header("Profile-Update-Interval", "24")
	c.Header("Profile-Title", u.Username)

	// Outage contingency (TOUCH-7): subscription text comments carry a backup notice
	// address and a backup domain list, so the client cache has its own escape channel
	// (issued before any disconnection; empty subscriptions get them too -- expired and
	// over-quota users need a way back the most).
	notes := h.outageNotes()

	switch target {
	case "v2ray":
		body, err := sub.PackV2Ray(entries, notes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
	case "clash":
		body, err := sub.PackClash(entries, notes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(body))
	case "singbox":
		// A sing-box outbounds array (built in the hy2 batch, five protocols completed in
		// the SB batch): clients just merge it into their outbounds section. TOUCH-7 escape
		// comments are omitted -- plain JSON has no comment position and inserting comment
		// lines breaks parsing; the contingency notes travel via the v2ray/clash targets
		// (the panel's in-site notice and the Herald out-of-band channel remain reachable).
		body, err := sub.PackSingBox(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(body))
	default:
		fail(c, http.StatusBadRequest, errors.New("target must be v2ray, clash or singbox"))
	}
}

// rttWindow is the window of tunnel probe verdicts that participate in entry speed aggregation.
const rttWindow = 30 * time.Minute

// entryRttMs aggregates the average RTT (ms, E-19) of recent reachable tunnel probes
// per entry. Entries with no data or unreachable entries are absent from the return
// value, and the subscription remarks then omit the latency. Aggregation failure does
// not block the subscription; output degrades to no-latency.
func (h *Handler) entryRttMs() map[uint]int {
	out := map[uint]int{}
	var rows []struct {
		NodeID uint
		Avg    float64
	}
	if err := h.db.Model(&storage.ProbeReport{}).
		Select("node_id, AVG(rtt_ms) AS avg").
		Where("target_kind = ? AND reachable = ? AND probed_at > ?", "tunnel", true, time.Now().Add(-rttWindow)).
		Group("node_id").Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.NodeID] = int(r.Avg + 0.5)
	}
	return out
}

func userinfoHeader(u storage.User, usage trafficUsage) string {
	s := fmt.Sprintf("upload=%d; download=%d; total=%d", usage.Rx, usage.Tx, u.QuotaBytes)
	if u.ExpiresAt != nil {
		s += fmt.Sprintf("; expire=%d", u.ExpiresAt.Unix())
	}
	return s
}
