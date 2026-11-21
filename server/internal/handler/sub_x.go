// 自建订阅下发（ferry-sub-filters，2026-11-21 用户拍板）：GET /sub/x/:token。
// 护栏与主订阅 /sub/:token 完全一致（限流、设备校验、用量头、三格式 UA 协商），
// 差异只在 token 来源与节点集定制：token 查 subscriptions 表；节点候选集用
// 主订阅相同的 SQL 条件查出全量后，在内存中应用 per-sub filter 与 config。
// 主订阅路径不动，老客户端零感知。
package handler

import (
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// subscriptionFiltered 处理 GET /sub/x/:token：
//   - token 查 subscriptions 表，未找到或停用 → 404（subMissLimiter 记失败，
//     与主订阅防枚举同口径，不泄露「存在但不是你的」）；
//   - 关联用户未找到（订阅悬挂）或停用 → 空列表照常返回（不是 403），
//     与主订阅对到期/超限用户的口径一致；停用用户不进设备校验（不给停用账号记设备）；
//   - 节点候选集与主订阅相同 SQL 条件，内存中应用 filter（region/labels/protocol
//     三白名单），再按 config（sort/max_nodes/remark_template/include_rtt）定制；
//   - 配额降档与主订阅同口径（生效中的联动行按其快照档线只出低成本档）。
func (h *Handler) subscriptionFiltered(c *gin.Context) {
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
	// 关联用户停用：照常返回空列表（非 403），用量头仍带（客户端能看到状态）。
	if !u.Enabled {
		h.subXDeliver(c, u, trafficUsage{}, []sub.Entry{})
		return
	}
	// 设备/IP 校验（hwid+IP 口径与主订阅完全一致）。
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
		// 候选集与主订阅相同条件；落地不下发，provisioning 不出订阅。
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
		// per-sub 三维度过滤（内存）；labels 解析失败视为无标签，不 500。
		entries = sub.ApplyFilter(entries, subFilterOf(s.Filter))
		// 配额联动降档（SAVE-6，与主订阅同口径）。
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

// subXDeliver 组装自建订阅响应：目标协商、用量头、三格式打包（与主订阅同口径，
// 主订阅路径不复用本函数以免其行为被改动）。
func (h *Handler) subXDeliver(c *gin.Context, u storage.User, usage trafficUsage, entries []sub.Entry) {
	target := c.Query("target")
	if target == "" {
		target = subTargetByUA(c.Request.UserAgent())
	}
	c.Header("Subscription-Userinfo", userinfoHeader(u, usage))
	c.Header("Profile-Update-Interval", "24")
	c.Header("Profile-Title", u.Username)
	// TOUCH-7 容灾注释常附：客户端缓存自带备用入口与公告地址。
	notes := h.outageNotes()
	switch target {
	case "v2ray":
		// per-sub sort 已在 ApplyConfig 内排好（缺省走区域分组序），
		// 用 Ordered 版避免 PackV2Ray 内部再次重排覆盖 sort 配置。
		body, err := sub.PackV2RayOrdered(entries, notes)
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
		// sing-box 纯 JSON 无注释位，容灾注释不带（与主订阅一致）。
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

// subFilterOf 解析落库 filter 文本；解析失败（历史脏数据）按不过滤处理，
// 不让一条坏行打断下发。
func subFilterOf(raw string) sub.Filter {
	f, err := sub.ParseFilter(raw)
	if err != nil {
		log.Printf("sub filter parse failed: %v", err)
		return sub.Filter{}
	}
	return f
}

// subConfigOf 解析落库 config 文本；解析失败按缺省（沿用现状）处理。
func subConfigOf(raw string) sub.Config {
	cfg, err := sub.ParseConfig(raw)
	if err != nil {
		log.Printf("sub config parse failed: %v", err)
		return sub.Config{}
	}
	return cfg
}
