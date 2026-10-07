package herald

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// HTTPSender 是 ferry→Herald 的投递腿（HERALD-2，《告警通道设计》§3）：
// POST {url}/events，Bearer 鉴权，2xx=Herald 接收成功（其后通道分发与重试
// 由 Herald 管，异步回执经 /api/internal/event-results 落 event_deliveries）。
// 载荷带 ferry 侧 outbox id（协议扩展字段），供回执关联。
func HTTPSender(url, token string) Sender {
	base := strings.TrimSuffix(url, "/")
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ev storage.Event) error {
		payload, err := json.Marshal(outgoingEvent{
			ID: ev.ID, Kind: ev.Kind, Severity: ev.Severity,
			Title: ev.Title, Body: ev.Body, Target: ev.Target,
			DedupKey: ev.DedupKey, Meta: json.RawMessage(ev.Meta),
			OccurredAt: ev.OccurredAt,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodPost, base+"/events", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("herald POST /events: HTTP %d", resp.StatusCode)
		}
		return nil
	}
}

// outgoingEvent 是投递载荷（设计稿 §3 示例 + id 扩展字段供回执关联）。
type outgoingEvent struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Severity   string          `json:"severity"`
	Title      string          `json:"title"`
	Body       string          `json:"body,omitempty"`
	Target     string          `json:"target"`
	DedupKey   string          `json:"dedup_key,omitempty"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}
