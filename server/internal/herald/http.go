package herald

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// HTTPSender is the ferry→Herald delivery leg (HERALD-2, "alert channel design" §3):
// POST {url}/events with Bearer auth; 2xx means Herald accepted (channel dispatch and retries from there
// are Herald's, with async receipts landing in event_deliveries via /api/internal/event-results).
// The payload carries the ferry-side outbox id (a protocol extension field) so receipts can be correlated.
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
		// Pacing (F-0047): the shared per-host pacer keeps the delivery loop
		// inside the channel's tolerance; the wait is bounded by the window.
		if err := ratelimit.WaitURL(context.Background(), base+"/events"); err != nil {
			return err
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

// outgoingEvent is the delivery payload (design §3 example plus the id extension field for receipt correlation).
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
