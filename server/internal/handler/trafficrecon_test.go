package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestTrafficRecon covers the flow-account audit view (F-1253): per-node ledger
// sums inside the window, the double_billing flag when the user ledger exceeds
// the node's own metering (the single-end-metering invariant, F-1242/F-1243),
// window filtering in both directions, and days validation.
func TestTrafficRecon(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		t.Helper()
		if rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"role": "entry",
		}); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body)
		}
		var n storage.Node
		if err := db.Where("name = ?", name).First(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	okID := mkNode("rc-ok")
	badID := mkNode("rc-bad")

	// A real user: traffic_logs carries a FK to users, so the ledger rows need a live id.
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "rc-user", "quota_bytes": 1 << 30}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	if err := db.Where("username = ?", "rc-user").First(&u).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	seedTraffic := func(nodeID uint, rx int64, at time.Time) {
		t.Helper()
		nid, uid := nodeID, u.ID
		if err := db.Create(&storage.TrafficLog{UserID: &uid, NodeID: &nid, RxBytes: rx,
			RecordedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seedNode := func(nodeID uint, rx int64, at time.Time) {
		t.Helper()
		if err := db.Create(&storage.NodeTrafficLog{NodeID: nodeID, Proc: "xray", RxBytes: rx,
			RecordedAt: at}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// rc-ok: billed 150 inside a 200-metered node -- healthy, delta 50.
	seedTraffic(okID, 100, now)
	seedTraffic(okID, 50, now)
	seedNode(okID, 200, now)
	// Outside the default window: excluded from both ledgers.
	seedTraffic(okID, 9000, now.AddDate(0, 0, -10))
	seedNode(okID, 9000, now.AddDate(0, 0, -10))
	// rc-bad: the node's own metering (150) is below what it billed (300) --
	// impossible under single-end metering, flagged.
	seedTraffic(badID, 300, now)
	seedNode(badID, 150, now)

	rec := doJSON(t, r, "GET", "/api/traffic-recon", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("recon: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		WindowDays int `json:"window_days"`
		Flagged    int `json:"flagged"`
		Nodes      []struct {
			NodeID      uint   `json:"node_id"`
			NodeName    string `json:"node_name"`
			UserBilled  int64  `json:"user_billed"`
			NodeMetered int64  `json:"node_metered"`
			Delta       int64  `json:"delta"`
			Status      string `json:"status"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WindowDays != 7 || got.Flagged != 1 || len(got.Nodes) != 2 {
		t.Fatalf("report head: %+v", got)
	}
	if got.Nodes[0].NodeID > got.Nodes[1].NodeID {
		t.Fatalf("rows must be node-id sorted: %+v", got.Nodes)
	}
	var okRow, badRow *struct {
		NodeID      uint   `json:"node_id"`
		NodeName    string `json:"node_name"`
		UserBilled  int64  `json:"user_billed"`
		NodeMetered int64  `json:"node_metered"`
		Delta       int64  `json:"delta"`
		Status      string `json:"status"`
	}
	for i := range got.Nodes {
		if got.Nodes[i].NodeID == okID {
			n := got.Nodes[i]
			okRow = &n
		}
		if got.Nodes[i].NodeID == badID {
			n := got.Nodes[i]
			badRow = &n
		}
	}
	if okRow == nil || badRow == nil {
		t.Fatalf("rows missing: %+v", got.Nodes)
	}
	if okRow.UserBilled != 150 || okRow.NodeMetered != 200 || okRow.Delta != 50 || okRow.Status != "ok" || okRow.NodeName != "rc-ok" {
		t.Fatalf("ok row: %+v", okRow)
	}
	if badRow.UserBilled != 300 || badRow.NodeMetered != 150 || badRow.Status != "double_billing" {
		t.Fatalf("bad row: %+v", badRow)
	}

	// The window shrinks with ?days=: at days=2 the 3-day-old row (40) drops
	// out alongside the 10-day-old, leaving only today's 150.
	seedTraffic(okID, 40, now.AddDate(0, 0, -3))
	if rec := doJSON(t, r, "GET", "/api/traffic-recon?days=2", nil); rec.Code != http.StatusOK {
		t.Fatalf("recon days=2: %d", rec.Code)
	} else if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, row := range got.Nodes {
		if row.NodeID == okID && row.UserBilled != 150 {
			t.Fatalf("days=2 ok row billed = %d, want 150 (today's rows only)", row.UserBilled)
		}
	}

	// days validation: 0 is rejected.
	if rec := doJSON(t, r, "GET", "/api/traffic-recon?days=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("days=0: %d", rec.Code)
	}
}
