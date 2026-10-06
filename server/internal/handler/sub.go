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

// subscription 处理 GET /sub/:token（P0-6/P0-7/P0-8）：
// 未启用用户按 404 处理（令牌视同吊销，防枚举）；到期/超限返回空订阅+用量头。
// target=v2ray|clash 显式指定，缺省按 UA 识别（含 clash 走 clash，其余 v2ray）。
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
	if err := h.db.Model(&storage.TrafficLog{}).
		Select("COALESCE(SUM(rx_bytes),0) AS rx, COALESCE(SUM(tx_bytes),0) AS tx").
		Where("user_id = ?", u.ID).Scan(&usage).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	var nodes []storage.Node
	if sub.UserActive(&u, usage.Rx+usage.Tx, time.Now()) {
		if err := h.db.Where("enabled = ?", true).Order("id").Find(&nodes).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
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
		body, err := sub.PackV2Ray(nodes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body))
	case "clash":
		body, err := sub.PackClash(nodes)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(body))
	default:
		fail(c, http.StatusBadRequest, errors.New("target must be v2ray or clash"))
	}
}

func userinfoHeader(u storage.User, usage trafficUsage) string {
	s := fmt.Sprintf("upload=%d; download=%d; total=%d", usage.Rx, usage.Tx, u.QuotaBytes)
	if u.ExpiresAt != nil {
		s += fmt.Sprintf("; expire=%d", u.ExpiresAt.Unix())
	}
	return s
}
