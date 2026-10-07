package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/sub"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// trafficUsage 汇总某用户的累计用量。
type trafficUsage struct {
	Rx int64
	Tx int64
}

// subscription 处理 GET /sub/:token（P0-6/P0-7/P0-8/E-19）：
// 未启用用户按 404 处理（令牌视同吊销，防枚举）；到期/超限返回空订阅+用量头；
// 入口列表按区域分组、备注带聚合测速延迟。target=v2ray|clash 显式指定，
// 缺省按 UA 识别（含 clash 走 clash，其余 v2ray）。
func (h *Handler) subscription(c *gin.Context) {
	var u storage.User
	if err := h.db.Where("sub_token = ?", c.Param("token")).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !u.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
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
		// 订阅下发入口列表（E-19）：仅 entry/both 角色节点；落地不下发。
		var nodes []storage.Node
		if err := h.db.Where("enabled = ? AND role IN (?, ?)", true, "entry", "both").
			Order("id").Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		rtt := h.entryRttMs()
		entries = make([]sub.Entry, 0, len(nodes))
		for _, n := range nodes {
			entries = append(entries, sub.Entry{Node: n, RttMs: rtt[n.ID]})
		}
	}

	target := c.Query("target")
	if target == "" {
		if strings.Contains(strings.ToLower(c.Request.UserAgent()), "clash") {
			target = "clash"
		} else {
			target = "v2ray"
		}
	}

	// P0-7 头：用量与到期（节点收到的 rx 记用户上传，tx 记下载）。
	c.Header("Subscription-Userinfo", userinfoHeader(u, usage))
	c.Header("Profile-Update-Interval", "24")
	c.Header("Profile-Title", u.Username)

	switch target {
	case "v2ray":
		body, err := sub.PackV2Ray(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
	case "clash":
		body, err := sub.PackClash(entries)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(body))
	default:
		fail(c, http.StatusBadRequest, errors.New("target must be v2ray or clash"))
	}
}

// rttWindow 内的隧道探测结论参与入口测速聚合。
const rttWindow = 30 * time.Minute

// entryRttMs 聚合每个入口近期可达隧道探测的平均 RTT（毫秒，E-19）。
// 无数据或不可达的入口不出现在返回值里，订阅备注随之省略延迟。
// 聚合失败不阻断订阅，退化成无延迟输出。
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
