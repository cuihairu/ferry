package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// 通知渠道管理（P1-10）：Webhook 地址与共享密钥存 settings KV，
// 改动即时生效（每次外发现读），无需重启。

// getNotify 查看通知渠道（GET /admin/notify）；密钥只回是否已配置。
func (h *Handler) getNotify(c *gin.Context) {
	url, ok, err := storage.GetSetting(h.db, notify.KeyURL)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	secretConfigured := false
	if _, ok, err := storage.GetSetting(h.db, notify.KeySecret); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	} else if ok {
		secretConfigured = true
	}
	c.JSON(http.StatusOK, gin.H{
		"url":               urlIfConfigured(url, ok),
		"secret_configured": secretConfigured,
		"enabled":           url != "",
	})
}

func urlIfConfigured(url string, ok bool) string {
	if !ok {
		return ""
	}
	return url
}

// putNotify 保存通知渠道（PUT /admin/notify）：url 空串表示停用；
// secret 空串表示清除。
func (h *Handler) putNotify(c *gin.Context) {
	var in struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" && !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		fail(c, http.StatusBadRequest, errors.New("url must start with http:// or https://"))
		return
	}
	if err := storage.SetSetting(h.db, notify.KeyURL, in.URL); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, notify.KeySecret, strings.TrimSpace(in.Secret)); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "enabled": in.URL != ""})
}

// testNotify 发送测试事件（POST /admin/notify/test）：走与真实事件相同的
// 外发管线，接收端可达性与密钥配置当场验证。
func (h *Handler) testNotify(c *gin.Context) {
	n := notify.FromDB(h.db)
	if !n.Enabled() {
		fail(c, http.StatusBadRequest, errors.New("webhook not configured"))
		return
	}
	if err := n.Send(notify.Event{Event: "test", Text: "ferry 通知渠道测试"}); err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true})
}
