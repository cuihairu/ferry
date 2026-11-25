// Package notify is outbound webhooks for events (P1-10, source: Marzban's Telegram Bot and
// 3x-ui's discord_notify_job alerting policy): once a webhook address is configured in settings, key events are
// POSTed out as JSON; unconfigured is a no-op. Channels such as Telegram can be adapted by forwarding on the receiving end.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// settings keys: the webhook address and the signing shared secret (the secret is sent as a request header; empty means none).
const (
	KeyURL    = "notify_webhook_url"
	KeySecret = "notify_webhook_secret"

	// sendTimeout bounds a single outbound duration, avoiding dragging down the periodic scan.
	sendTimeout = 5 * time.Second
)

// Event is the outbound event payload: text is a one-line human-readable summary, fields is structured supplement.
type Event struct {
	Event  string         `json:"event"`
	Text   string         `json:"text"`
	Time   time.Time      `json:"time"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Notifier is the webhook notifier; the zero value is a no-op.
type Notifier struct {
	url    string
	secret string
	client *http.Client
}

// FromDB reads the current notification channel from settings; unconfigured returns a no-op Notifier (read fresh each time,
// so a channel change takes effect immediately without a restart).
func FromDB(db *gorm.DB) *Notifier {
	url, ok, err := storage.GetSetting(db, KeyURL)
	if err != nil || !ok || url == "" {
		return &Notifier{}
	}
	secret, _, _ := storage.GetSetting(db, KeySecret)
	return &Notifier{url: url, secret: secret, client: &http.Client{Timeout: sendTimeout}}
}

// Enabled reports whether the channel is configured.
func (n *Notifier) Enabled() bool { return n != nil && n.url != "" }

// Send pushes one event out; unconfigured returns nil directly; a failure returns the error for the caller to log.
func (n *Notifier) Send(e Event) error {
	if !n.Enabled() {
		return nil
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.secret != "" {
		req.Header.Set("X-Ferry-Webhook-Secret", n.secret)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}
