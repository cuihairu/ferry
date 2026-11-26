package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathTrafficDimension covers the path-dimension traffic view (F-1248):
// an out-direction path reads the entry's metered ledger (the user-inbound end,
// F-1242/F-1243), an in-direction path reads the landing's, the other members'
// rows never leak in, days group like the per-user view, and an unknown path 404s.
func TestPathTrafficDimension(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name, role string) uint {
		t.Helper()
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"role": role,
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
		var n storage.Node
		if err := db.Where("name = ?", name).First(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry := mkNode("pt-ent", "entry")
	landing := mkNode("pt-lnd", "landing")

	if rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "pt-out", "entry_node_id": entry, "landing_node_id": landing,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create out path: %d %s", rec.Code, rec.Body)
	}
	var p storage.Path
	if err := db.Where("name = ?", "pt-out").First(&p).Error; err != nil {
		t.Fatal(err)
	}

	day1 := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 27, 21, 0, 0, 0, time.UTC)
	entryID, landingID := entry, landing
	seed := []storage.TrafficLog{
		{UserID: nil, NodeID: &entryID, RxBytes: 100, TxBytes: 10, RecordedAt: day1},
		{UserID: nil, NodeID: &entryID, RxBytes: 30, TxBytes: 5, RecordedAt: day1.Add(time.Hour)},
		{UserID: nil, NodeID: &entryID, RxBytes: 50, TxBytes: 7, RecordedAt: day2},
		// The landing's tunnel-side copy must not leak into the out-direction view.
		{UserID: nil, NodeID: &landingID, RxBytes: 9999, TxBytes: 9999, RecordedAt: day1},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, "GET", "/api/paths/"+strconv.Itoa(int(p.ID))+"/traffic", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get path traffic: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		PathID      uint   `json:"path_id"`
		Direction   string `json:"direction"`
		MeterNodeID uint   `json:"meter_node_id"`
		TotalRx     int64  `json:"total_rx"`
		TotalTx     int64  `json:"total_tx"`
		Days        []struct {
			Date string `json:"date"`
			Rx   int64  `json:"rx"`
			Tx   int64  `json:"tx"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PathID != p.ID || got.Direction != "out" || got.MeterNodeID != entry {
		t.Fatalf("summary head: %+v", got)
	}
	if got.TotalRx != 180 || got.TotalTx != 22 {
		t.Fatalf("totals: %+v", got)
	}
	if len(got.Days) != 2 || got.Days[0].Date != "2026-11-26" || got.Days[0].Rx != 130 || got.Days[0].Tx != 15 ||
		got.Days[1].Date != "2026-11-27" || got.Days[1].Rx != 50 || got.Days[1].Tx != 7 {
		t.Fatalf("days: %+v", got.Days)
	}

	// In-direction: the metering end flips to the landing node, and the
	// entry-side rows must not leak in.
	if rec := doJSON(t, r, "POST", "/api/paths", map[string]any{
		"name": "pt-in", "entry_node_id": entry, "landing_node_id": landing, "direction": "in",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create in path: %d %s", rec.Code, rec.Body)
	}
	var pin storage.Path
	if err := db.Where("name = ?", "pt-in").First(&pin).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/paths/"+strconv.Itoa(int(pin.ID))+"/traffic", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get in path traffic: %d %s", rec.Code, rec.Body)
	}
	var gotIn map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &gotIn); err != nil {
		t.Fatal(err)
	}
	if gotIn["meter_node_id"].(float64) != float64(landing) {
		t.Fatalf("in-direction meter node: %+v", gotIn)
	}
	if gotIn["total_rx"].(float64) != 9999 {
		t.Fatalf("in-direction totals: %+v", gotIn)
	}

	// Unknown path: 404.
	if rec := doJSON(t, r, "GET", "/api/paths/424242/traffic", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", rec.Code)
	}
}
