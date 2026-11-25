package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// Notification channel management (P1-10): the Webhook URL and shared secret
// are stored in the settings KV, changes take effect immediately (read on
// every outbound send), with no restart required.

// getNotify reads the notification channel (GET /admin/notify); the secret is
// reported only as configured-or-not.
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

// putNotify saves the notification channel (PUT /admin/notify): an empty url
// means disabled; an empty secret means clear it.
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

// testNotify sends a test event (POST /admin/notify/test): it goes through the
// same outbound pipeline as real events, verifying receiver reachability and
// secret configuration on the spot.
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
