package handler

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/policy"
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
// explicitly given): the Clash.Meta/mihomo family gets the mihomo YAML, plain clash
// clients the conservative clash YAML, sing-box native and Stash get the sing-box
// outbounds JSON, xray-family clients the xray-json outbounds, and everything else
// falls back to v2ray base64 (SHOP-2; the mihomo and xray-json targets are G1). The
// meta tokens are matched before the bare "clash" token because a mihomo UA also
// carries it. Shared by the main subscription and the custom subscriptions.
func subTargetByUA(ua string) string {
	ua = strings.ToLower(ua)
	switch {
	case strings.Contains(ua, "clash.meta"), strings.Contains(ua, "clashmeta"),
		strings.Contains(ua, "mihomo"), strings.Contains(ua, "flclash"),
		strings.Contains(ua, "verge"):
		return "mihomo"
	case strings.Contains(ua, "clash"):
		return "clash"
	case strings.Contains(ua, "sing-box"), strings.Contains(ua, "stash"):
		return "singbox"
	case strings.Contains(ua, "xray"):
		return "xray"
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
	// Access audit (F-1150): deferred, so every outcome -- 200/304 delivery, 404
	// unknown/revoked token, 429 rate limit, 403 device check -- lands one row.
	defer h.recordSubAccess(c)
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
	c.Set("subUserID", u.ID)
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
		// online, at which point they enter the list automatically. Plan permission
		// (F-1142): a plan-bound node (Node.PlanID) delivers only to that plan's
		// subscribers; a plan-less node serves everyone.
		var nodes []storage.Node
		if err := h.db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND pool_state = ? AND status != ? AND (plan_id = 0 OR plan_id = ?)",
			true, false, "entry", "both", pool.StateActive, "provisioning", u.PlanID).
			Order("id").Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		rtt := h.entryRttMs()
		// Scoped policy constraints (F-0282~F-0293): system/plan/user policies
		// filter the entry candidates before any output shaping (no policies
		// on the chain = zero change). The plan scope rides the user's own
		// PlanID (F-1207): plan-scoped region/cost constraints bind exactly
		// that plan's subscribers, plan-less users only see the system scope.
		eff, _, err := policy.Resolve(h.db, u.ID, u.PlanID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		nodes, _ = policy.ApplyConstraints(nodes, eff)
		entries = make([]sub.Entry, 0, len(nodes))
		for _, n := range nodes {
			entries = append(entries, sub.Entry{Node: n, RttMs: rtt[n.ID]})
		}
		// Group↔subscription policy link (F-0270): a policy chain pinning a
		// path group serves only the resolved serving path's entry node --
		// the group picks the path (default first, then candidates by
		// position, health-aware); the entry must still pass the constraints
		// above. Nothing usable resolves to an empty subscription.
		if eff.PathGroupID != 0 {
			resolved, err := pathhealth.ResolveGroupPath(h.db, eff.PathGroupID, time.Now())
			if err != nil {
				fail(c, http.StatusInternalServerError, err)
				return
			}
			passing := map[uint]bool{}
			for _, n := range nodes {
				passing[n.ID] = true
			}
			filtered := entries[:0]
			if resolved != nil && passing[resolved.EntryNodeID] {
				for _, e := range entries {
					if e.Node.ID == resolved.EntryNodeID {
						filtered = append(filtered, e)
					}
				}
			}
			entries = filtered
		}
		// Output shaping (F-0286): compact caps the delivered list to the
		// probe-sorted best ten.
		if eff.Output == "compact" {
			compact := policy.CapOutput(nodes, rtt, eff)
			keep := map[uint]bool{}
			for _, n := range compact {
				keep[n.ID] = true
			}
			filtered := entries[:0]
			for _, e := range entries {
				if keep[e.Node.ID] {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
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

	// Outage contingency (TOUCH-7): subscription text comments carry a backup notice
	// address and a backup domain list, so the client cache has its own escape channel
	// (issued before any disconnection; empty subscriptions get them too -- expired and
	// over-quota users need a way back the most).
	notes := h.outageNotes()

	var body, ctype string
	switch target {
	case "v2ray":
		b, err := sub.PackV2Ray(entries, notes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "text/plain; charset=utf-8"
	case "clash":
		b, err := sub.PackClash(entries, notes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "text/yaml; charset=utf-8"
	case "mihomo":
		b, err := sub.PackMihomo(entries, notes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "text/yaml; charset=utf-8"
	case "xray":
		// xray-json: an Xray-core outbounds array (G1). Plain JSON has no comment
		// position, so the contingency notes are omitted, same reasoning as singbox.
		b, err := sub.PackXray(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "application/json; charset=utf-8"
	case "singbox":
		// A sing-box outbounds array (built in the hy2 batch, five protocols completed in
		// the SB batch): clients just merge it into their outbounds section. TOUCH-7 escape
		// comments are omitted -- plain JSON has no comment position and inserting comment
		// lines breaks parsing; the contingency notes travel via the v2ray/clash targets
		// (the panel's in-site notice and the Herald out-of-band channel remain reachable).
		b, err := sub.PackSingBox(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "application/json; charset=utf-8"
	default:
		fail(c, http.StatusBadRequest, errors.New("target must be v2ray, clash, mihomo, singbox or xray"))
		return
	}
	// P0-7 usage headers + unified version/update-time metadata (F-1108) and
	// the conditional-request short-circuit (F-1148).
	if subscriptionMeta(c, u, usage, []byte(body)) {
		c.Data(http.StatusOK, ctype, []byte(body))
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

// recordSubAccess writes the subscription access audit row (F-1150). Both subscription
// surfaces defer it, so every outcome is recorded: 200/304 deliveries, 404 misses
// (unknown or revoked token), 429 rate limiting, 403 device checks. The row keys on
// the resolved user (c.Get "subUserID", 0 when the token never resolved) and never
// carries the raw token; a write failure degrades to a log line and never blocks
// delivery.
func (h *Handler) recordSubAccess(c *gin.Context) {
	uid, _ := c.Get("subUserID")
	id, _ := uid.(uint)
	target := c.Query("target")
	if target == "" {
		target = subTargetByUA(c.Request.UserAgent())
	}
	ua := c.Request.UserAgent()
	if len(ua) > 255 {
		ua = ua[:255]
	}
	if len(target) > 32 {
		target = target[:32]
	}
	row := storage.SubAccessLog{
		UserID: id,
		Path:   c.FullPath(),
		Target: target,
		UA:     ua,
		IP:     c.ClientIP(),
		Code:   c.Writer.Status(),
	}
	if err := h.db.Create(&row).Error; err != nil {
		log.Printf("sub access log write failed: %v", err)
	}
}

func userinfoHeader(u storage.User, usage trafficUsage) string {
	s := fmt.Sprintf("upload=%d; download=%d; total=%d", usage.Rx, usage.Tx, u.QuotaBytes)
	if u.ExpiresAt != nil {
		s += fmt.Sprintf("; expire=%d", u.ExpiresAt.Unix())
	}
	return s
}

// subscriptionMeta writes the unified subscription response metadata (F-1108):
// the usage headers plus the update time (Last-Modified, UTC) and the content
// version stamp (X-Subscription-Version, an sha1 prefix -- same body, same
// version). It also implements the conditional-request protocol (F-1148): the
// ETag is the body's full sha1, and an If-None-Match hit serves 304 with no
// body -- the metadata headers still go out so the client can bookkeep the
// refresh. Returns false when a 304 was written and the caller must not
// write the body.
func subscriptionMeta(c *gin.Context, u storage.User, usage trafficUsage, body []byte) bool {
	c.Header("Subscription-Userinfo", userinfoHeader(u, usage))
	c.Header("Profile-Update-Interval", "24")
	c.Header("Profile-Title", u.Username)
	c.Header("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	// Cache invalidation (F-1149): shared caches must not serve a stale body.
	// Revalidation against the content ETag is cheap (a 304), so no-cache is
	// safe; the content hash moves on every fleet change, which is the
	// invalidation itself.
	c.Header("Cache-Control", "no-cache")
	sum := sha1.Sum(body)
	full := hex.EncodeToString(sum[:])
	c.Header("ETag", `"`+full+`"`)
	c.Header("X-Subscription-Version", full[:12])
	if c.GetHeader("If-None-Match") == `"`+full+`"` {
		c.Status(http.StatusNotModified)
		return false
	}
	return true
}
