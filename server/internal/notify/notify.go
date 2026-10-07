// Package notify 事件外发 Webhook（P1-10，来源：Marzban Telegram Bot、
// 3x-ui discord_notify_job 的告警外发口径）：设置里配置 Webhook 地址后，
// 关键事件以 JSON POST 外发；未配置为空操作。Telegram 等渠道可由接收端转发适配。
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

// settings 键：Webhook 地址与签名用共享密钥（密钥作请求头外发，空=不带）。
const (
	KeyURL    = "notify_webhook_url"
	KeySecret = "notify_webhook_secret"

	// sendTimeout 限制单次外发时长，避免拖慢周期扫表。
	sendTimeout = 5 * time.Second
)

// Event 是外发事件载荷：text 为一行人类可读摘要，fields 为结构化补充。
type Event struct {
	Event  string         `json:"event"`
	Text   string         `json:"text"`
	Time   time.Time      `json:"time"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Notifier 是 Webhook 通知器；零值实例为空操作。
type Notifier struct {
	url    string
	secret string
	client *http.Client
}

// FromDB 从设置读取当前通知渠道；未配置返回空操作 Notifier（每次现读，
// 渠道改动即时生效，无需重启）。
func FromDB(db *gorm.DB) *Notifier {
	url, ok, err := storage.GetSetting(db, KeyURL)
	if err != nil || !ok || url == "" {
		return &Notifier{}
	}
	secret, _, _ := storage.GetSetting(db, KeySecret)
	return &Notifier{url: url, secret: secret, client: &http.Client{Timeout: sendTimeout}}
}

// Enabled 表示渠道是否已配置。
func (n *Notifier) Enabled() bool { return n != nil && n.url != "" }

// Send 外发一条事件；未配置直接返回 nil；失败返回错误由调用方记日志。
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
