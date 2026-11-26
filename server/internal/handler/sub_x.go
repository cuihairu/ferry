// Custom subscription delivery (ferry-sub-filters, decided by the user on 2026-11-21):
// GET /sub/x/:token.
// The guardrails are identical to the main subscription /sub/:token (rate limiting,
// device checks, usage headers, five-format UA negotiation); the only differences
// are the token source and the node-set customization: the token is looked up in the
// subscriptions table; the node candidate set is fetched in full with the same SQL
// conditions as the main subscription, and then per-sub filter and config are applied
// in memory.
// The main subscription path is untouched, so old clients see nothing.
package handler

import (
	"errors"
	"log"
	"net/http"
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

// subscriptionFiltered handles GET /sub/x/:token:
//   - the token is looked up in the subscriptions table; missing or disabled -> 404
//     (subMissLimiter records the failure, same as the main subscription's
//     anti-enumeration stance; it does not leak "exists but not yours");
//   - a missing linked user (dangling subscription) or a disabled one -> the empty
//     list is returned as usual (not a 403), consistent with the main subscription's
//     handling of expired/over-quota users; disabled users skip device checks (no
//     devices are recorded for a disabled account);
//   - the node candidate set uses the same SQL conditions as the main subscription;
//     the filter (region/labels/protocol three-way whitelist) is applied in memory,
//     then config (sort/max_nodes/remark_template/include_rtt) customizes the result;
//   - quota downgrade follows the main subscription's semantics (active linkage rows
//     serve only the low-cost tier per their snapshot tier line).
func (h *Handler) subscriptionFiltered(c *gin.Context) {
	// Access audit (F-1150), the same deferred record as the main subscription.
	defer h.recordSubAccess(c)
	token, ip := c.Param("token"), c.ClientIP()
	if !h.subMissLimiter.Allow(ip) || !h.subLimiter.Allow(token) {
		log.Printf("sub rate limited: token=%s… ip=%s", token[:min(6, len(token))], ip)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
		return
	}
	var s storage.Subscription
	if err := h.db.Where("token = ?", token).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.subMissLimiter.RecordFailure(ip)
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !s.Enabled {
		h.subMissLimiter.RecordFailure(ip)
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var u storage.User
	if err := h.db.First(&u, s.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			h.subMissLimiter.RecordFailure(ip)
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Set("subUserID", u.ID)
	// Linked user disabled: return the empty list as usual (not a 403); usage headers
	// are still attached so the client can see its status.
	if !u.Enabled {
		h.subXDeliver(c, u, trafficUsage{}, []sub.Entry{})
		return
	}
	// Device/IP checks (hwid+IP semantics identical to the main subscription).
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
		// The candidate set uses the same conditions as the main subscription; landing
		// nodes are not delivered, and provisioning nodes stay out of the subscription.
		// Plan permission (F-1142) rides the same WHERE: plan-bound nodes only for
		// that plan's subscribers, plan-less nodes for everyone.
		var nodes []storage.Node
		if err := h.db.Where("enabled = ? AND maintenance = ? AND role IN (?, ?) AND pool_state = ? AND status != ? AND (plan_id = 0 OR plan_id = ?)",
			true, false, "entry", "both", pool.StateActive, "provisioning", u.PlanID).
			Order("id").Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		rtt := h.entryRttMs()
		// Scoped policy constraints (F-0282~F-0293), the same chain as the
		// main subscription so the two surfaces cannot drift (F-1144); the
		// plan scope rides the user's PlanID as there (F-1207).
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
		// Group↔subscription policy link (F-0270), the same pin as the main
		// subscription (F-1143): a policy chain pinning a path group serves
		// only the resolved serving path's entry node. Nothing usable
		// resolves to an empty subscription.
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
		// Per-sub three-dimension filtering (in memory); a labels parse failure counts as
		// no labels and never becomes a 500.
		entries = sub.ApplyFilter(entries, subFilterOf(s.Filter))
		// Quota-linked downgrade (SAVE-6, same semantics as the main subscription).
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
		entries = sub.ApplyConfig(entries, subConfigOf(s.Config), time.Now())
	}
	h.subXDeliver(c, u, usage, entries)
}

// subXDeliver assembles the custom subscription response: target negotiation, usage
// headers, five-format packing (same semantics as the main subscription; the main
// subscription path does not reuse this function so its behavior cannot drift).
func (h *Handler) subXDeliver(c *gin.Context, u storage.User, usage trafficUsage, entries []sub.Entry) {
	target := c.Query("target")
	if target == "" {
		target = subTargetByUA(c.Request.UserAgent())
	}
	// TOUCH-7 contingency notes are always attached: the client cache carries its own
	// backup entries and notice address.
	notes := h.outageNotes()
	var body, ctype string
	switch target {
	case "v2ray":
		// The per-sub sort was already applied inside ApplyConfig (the default is the
		// region-grouped order); the Ordered variant avoids PackV2Ray re-sorting
		// internally and overriding the sort config.
		b, err := sub.PackV2RayOrdered(entries, notes)
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
		// xray-json: an Xray-core outbounds array (G1); plain JSON has no comment position, same omission reasoning as singbox.
		b, err := sub.PackXray(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		body, ctype = b, "application/json; charset=utf-8"
	case "singbox":
		// sing-box is plain JSON with no comment position, so contingency notes are omitted (consistent with the main subscription).
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
	// Usage headers + unified version/update-time metadata (F-1108, one helper
	// with the main subscription) and the conditional-request short-circuit
	// (F-1148).
	if subscriptionMeta(c, u, usage, []byte(body)) {
		c.Data(http.StatusOK, ctype, []byte(body))
	}
}

// subFilterOf parses the stored filter text; a parse failure (legacy dirty data) is
// treated as no filtering, so one bad row never interrupts delivery.
func subFilterOf(raw string) sub.Filter {
	f, err := sub.ParseFilter(raw)
	if err != nil {
		log.Printf("sub filter parse failed: %v", err)
		return sub.Filter{}
	}
	return f
}

// subConfigOf parses the stored config text; a parse failure falls back to the default (status quo).
func subConfigOf(raw string) sub.Config {
	cfg, err := sub.ParseConfig(raw)
	if err != nil {
		log.Printf("sub config parse failed: %v", err)
		return sub.Config{}
	}
	return cfg
}
