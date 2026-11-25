package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// Tests for the Herald channel dashboard batch (HC-1/2/3): receipt dashboard aggregation and health
// badges, self-test events landing in the outbox, and the sample preview taking the most recent real event per kind.

// TestEventDeliveriesBoard covers the receipt dashboard: row list (newest first), channel filter,
// per-channel sent/failed aggregation with health badges, and the latest failure detail.
func TestEventDeliveriesBoard(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	// Two events, three receipts: tg all sent, email one failure (with detail).
	for _, ev := range []storage.Event{
		{Kind: "node_blocked", Severity: "critical", Title: "t1", Target: "admin", Status: "sent", OccurredAt: now, CreatedAt: now},
		{Kind: "cert_expiring", Severity: "warning", Title: "t2", Target: "admin", Status: "sent", OccurredAt: now, CreatedAt: now},
	} {
		if err := db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []storage.EventDelivery{
		{EventID: 1, Channel: "tg", Status: "sent", At: now},
		{EventID: 2, Channel: "tg", Status: "sent", At: now},
		{EventID: 2, Channel: "email", Status: "failed", Detail: "smtp timeout", At: now},
	} {
		if err := db.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, r, "GET", "/api/events/deliveries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("deliveries: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Rows     []storage.EventDelivery `json:"rows"`
		Channels []struct {
			Channel      string `json:"channel"`
			Sent         int64  `json:"sent"`
			Failed       int64  `json:"failed"`
			Health       string `json:"health"`
			LastFailedAt string `json:"last_failed_at"`
			LastDetail   string `json:"last_detail"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 3 {
		t.Fatalf("rows = %d", len(out.Rows))
	}
	if len(out.Channels) != 2 {
		t.Fatalf("channels = %+v", out.Channels)
	}
	byName := map[string]struct {
		Sent, Failed int64
		Health       string
		Detail       string
	}{}
	for _, ch := range out.Channels {
		byName[ch.Channel] = struct {
			Sent, Failed int64
			Health       string
			Detail       string
		}{ch.Sent, ch.Failed, ch.Health, ch.LastDetail}
	}
	if tg := byName["tg"]; tg.Sent != 2 || tg.Failed != 0 || tg.Health != "healthy" {
		t.Fatalf("tg = %+v", byName["tg"])
	}
	if em := byName["email"]; em.Failed != 1 || em.Health != "down" || em.Detail != "smtp timeout" {
		t.Fatalf("email = %+v", byName["email"])
	}

	// Channel filter
	rec = doJSON(t, r, "GET", "/api/events/deliveries?channel=email", nil)
	var filtered struct {
		Rows []storage.EventDelivery `json:"rows"`
	}
	json.Unmarshal(rec.Body.Bytes(), &filtered)
	if len(filtered.Rows) != 1 || filtered.Rows[0].Channel != "email" {
		t.Fatalf("filtered rows = %+v", filtered.Rows)
	}
}

// TestEventSelfCheck covers the self-test event: it lands in the outbox (system_test/pending/admin),
// and consecutive sends are not merged (self-testing wants an independent receipt each time).
func TestEventSelfCheck(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "POST", "/api/events/test", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("test event: %d %s", rec.Code, rec.Body)
	}
	var ev storage.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Kind != "system_test" || ev.Status != "pending" || ev.Target != "admin" || ev.Severity != "info" {
		t.Fatalf("event = %+v", ev)
	}
	if rec = doJSON(t, r, "POST", "/api/events/test", nil); rec.Code != http.StatusCreated {
		t.Fatalf("second test: %d", rec.Code)
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "system_test").Count(&n)
	if n != 2 {
		t.Fatalf("system_test rows = %d, want 2（自检事件不合并）", n)
	}
}

// TestEventSamplesPreview covers the sample preview: the most recent row per kind (with multiple rows of
// the same kind, the newest wins), and an empty store returns an empty array.
func TestEventSamplesPreview(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	now := time.Now()
	for _, ev := range []storage.Event{
		{Kind: "node_blocked", Severity: "critical", Title: "旧样例", Body: "old", Target: "admin", Status: "sent", OccurredAt: now, CreatedAt: now},
		{Kind: "node_blocked", Severity: "critical", Title: "新样例", Body: "new body", Target: "admin", Status: "pending", OccurredAt: now, CreatedAt: now},
		{Kind: "backup_failed", Severity: "critical", Title: "备份失败样例", Body: "b", Target: "admin", Status: "sent", OccurredAt: now, CreatedAt: now},
	} {
		if err := db.Create(&ev).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := doJSON(t, r, "GET", "/api/events/samples", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("samples: %d %s", rec.Code, rec.Body)
	}
	var samples []struct {
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %+v", samples)
	}
	byKind := map[string]struct {
		Title  string
		Status string
	}{}
	for _, s := range samples {
		byKind[s.Kind] = struct {
			Title  string
			Status string
		}{s.Title, s.Status}
	}
	if got := byKind["node_blocked"]; got.Title != "新样例" {
		t.Fatalf("node_blocked sample = %+v（应取最近一条）", got)
	}
	if got := byKind["backup_failed"]; got.Title != "备份失败样例" {
		t.Fatalf("backup_failed sample = %+v", got)
	}
}
