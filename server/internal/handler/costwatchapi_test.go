package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPriceWatchAPI covers watch CRUD: validation, unique-key 409, partial update, snapshot query
// and delete cascade, hit-dynamics assembly (latest/previous/change/at-target).
func TestPriceWatchAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Missing spec rejected.
	rec := doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "vultr"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing spec status=%d", rec.Code)
	}
	// Negative target price rejected.
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "vultr", "spec": "1c", "target_price": -5})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative target status=%d", rec.Code)
	}

	// Create (with region and target price).
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "vultr", "region": "tokyo", "spec": "1c", "target_price": 900})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body)
	}
	var w watchView
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.ID == 0 || !w.Enabled || w.TargetPrice != 900 || w.SnapshotDone {
		t.Fatalf("created = %+v", w)
	}

	// Duplicate key 409 (unique index idx_price_watch).
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "VULTR", "region": "tokyo", "spec": "1c"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", rec.Code, rec.Body)
	}

	// List before any snapshots: no dynamics.
	rec = doJSON(t, r, "GET", "/api/cost/watches", nil)
	var list []watchView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].LatestCents != nil || list[0].AtTarget {
		t.Fatalf("list before snapshots = %+v", list)
	}

	// Partial update: target price to 700 + disable.
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"target_price": 700, "enabled": false})
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.TargetPrice != 700 || w.Enabled {
		t.Fatalf("updated = %+v", w)
	}
	// Re-enable.
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable status=%d", rec.Code)
	}

	// Snapshot query: after inserting two rows, id DESC; list dynamics assemble latest 800 / previous 1000 / down 20% / at-target (target 700? no).
	db.Create(&storage.PriceSnapshot{WatchID: w.ID, MonthlyCents: 1000, Source: "pricelist", CapturedAt: time.Now().Add(-time.Hour)})
	db.Create(&storage.PriceSnapshot{WatchID: w.ID, MonthlyCents: 800, Source: "pricelist", CapturedAt: time.Now()})
	db.Create(&storage.PriceSnapshot{WatchID: 999, MonthlyCents: 12345, Source: "pricelist", CapturedAt: time.Now()}) // isolated from other keys

	rec = doJSON(t, r, "GET", "/api/cost/watches/1/snapshots", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshots status=%d", rec.Code)
	}
	var snaps []storage.PriceSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snaps); err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 || snaps[0].MonthlyCents != 800 || snaps[1].MonthlyCents != 1000 {
		t.Fatalf("snapshots = %+v", snaps)
	}

	rec = doJSON(t, r, "GET", "/api/cost/watches", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	w = list[0]
	if w.LatestCents == nil || *w.LatestCents != 800 || w.PrevCents == nil || *w.PrevCents != 1000 {
		t.Fatalf("watch dynamic = %+v", w)
	}
	if w.ChangePct == nil || *w.ChangePct != -20 {
		t.Fatalf("change pct = %+v", w.ChangePct)
	}
	if w.AtTarget { // target 700 not reached
		t.Fatalf("at_target should be false: %+v", w)
	}

	// Negative validation cases: bad payload, create-as-disabled, bad ids and payloads for update/delete/snapshots
	if rec := doRaw(t, r, "POST", "/api/cost/watches", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad json: %d", rec.Code)
	}
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "hetzner", "spec": "2c", "target_price": 900, "enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("create disabled: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.Enabled || w.ID == 0 {
		t.Fatalf("created disabled = %+v", w)
	}
	second := w.ID
	if rec := doJSON(t, r, "PUT", "/api/cost/watches/abc", map[string]any{"enabled": true}); rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad id: %d", rec.Code)
	}
	if rec := doRaw(t, r, "PUT", fmt.Sprintf("/api/cost/watches/%d", second), "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("update bad json: %d", rec.Code)
	}
	if rec := doJSON(t, r, "PUT", fmt.Sprintf("/api/cost/watches/%d", second), map[string]any{"target_price": -1}); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative update target: %d", rec.Code)
	}
	if rec := doJSON(t, r, "DELETE", "/api/cost/watches/abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete bad id: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/cost/watches/abc/snapshots", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("snapshots bad id: %d", rec.Code)
	}
	// Snapshot limit out of range: falls back to the default 20 without erroring
	rec = doJSON(t, r, "GET", fmt.Sprintf("/api/cost/watches/%d/snapshots?limit=500", second), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshots limit fallback: %d %s", rec.Code, rec.Body)
	}

	// At-target check and the zero-previous guard (second watch, target 900): snapshot 0 -> 500,
	// latest 500 <= 900 counts as at-target; no change percentage when the previous value is 0 (division-by-zero guard).
	db.Create(&storage.PriceSnapshot{WatchID: second, MonthlyCents: 0, Source: "pricelist", CapturedAt: time.Now().Add(-time.Hour)})
	db.Create(&storage.PriceSnapshot{WatchID: second, MonthlyCents: 500, Source: "pricelist", CapturedAt: time.Now()})
	rec = doJSON(t, r, "GET", "/api/cost/watches", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var got *watchView
	for i := range list {
		if list[i].ID == second {
			got = &list[i]
		}
	}
	if got == nil || got.LatestCents == nil || *got.LatestCents != 500 {
		t.Fatalf("second watch dynamics = %+v", got)
	}
	if !got.AtTarget {
		t.Fatalf("at_target should be true: %+v", got)
	}
	if got.PrevCents == nil || *got.PrevCents != 0 || got.ChangePct != nil {
		t.Fatalf("zero prev guard = %+v", got)
	}

	// Delete cascade: watch and snapshots deleted together.
	rec = doJSON(t, r, "DELETE", "/api/cost/watches/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d", rec.Code)
	}
	var cnt int64
	db.Model(&storage.PriceSnapshot{}).Where("watch_id = ?", 1).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("snapshots after delete = %d", cnt)
	}
	// After delete → 404.
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"enabled": true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after delete status=%d", rec.Code)
	}
}
