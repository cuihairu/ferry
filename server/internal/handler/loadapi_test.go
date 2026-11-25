package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestLoadAPI covers the load snapshot (E-25): the connection count sums the
// latest sample of each proc, throughput diffs the two latest samples, and
// utilization is the share of capacity; no sample gives util=-1.
func TestLoadAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	busy := storage.Node{Name: "busy", Token: "tok-busy", Role: "landing", BwDownMbps: 100, Enabled: true}
	if err := db.Create(&busy).Error; err != nil {
		t.Fatal(err)
	}
	idle := storage.Node{Name: "idle", Token: "tok-idle", Role: "entry", Enabled: true}
	if err := db.Create(&idle).Error; err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// busy: diffing two samples -> rx up 400MB over 60s -> 53.3Mbps;
	// the connection count takes the latest sample, 12.
	seed := func(rx, tx, conns int64, at time.Time) {
		if err := db.Create(&storage.NodeTrafficLog{
			NodeID: busy.ID, Proc: "relay", RxBytes: rx, TxBytes: tx, Conns: int(conns), RecordedAt: at,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed(100e6, 0, 10, now.Add(-2*time.Minute))
	seed(500e6, 0, 12, now.Add(-time.Minute))

	rec := doJSON(t, r, "GET", "/api/load", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get load status = %d body=%s", rec.Code, rec.Body)
	}
	var out []struct {
		NodeID       uint    `json:"node_id"`
		Conns        int     `json:"conns"`
		Mbps         float64 `json:"mbps"`
		CapacityMbps int     `json:"capacity_mbps"`
		UtilPct      int     `json:"util_pct"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode load resp: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("rows = %d", len(out))
	}
	byID := map[uint]struct {
		Conns        int
		Mbps         float64
		CapacityMbps int
		UtilPct      int
	}{}
	for _, row := range out {
		byID[row.NodeID] = struct {
			Conns        int
			Mbps         float64
			CapacityMbps int
			UtilPct      int
		}{row.Conns, row.Mbps, row.CapacityMbps, row.UtilPct}
	}
	busyRow := byID[busy.ID]
	if busyRow.Conns != 12 {
		t.Fatalf("busy conns = %d", busyRow.Conns)
	}
	if busyRow.Mbps < 50 || busyRow.Mbps > 56 {
		t.Fatalf("busy mbps = %f", busyRow.Mbps)
	}
	if busyRow.CapacityMbps != 100 || busyRow.UtilPct < 50 || busyRow.UtilPct > 56 {
		t.Fatalf("busy util = %+v", busyRow)
	}
	// idle: no samples, util=-1.
	if byID[idle.ID].UtilPct != -1 {
		t.Fatalf("idle util = %d", byID[idle.ID].UtilPct)
	}
}

// TestPoolSuspendAPI covers manual pool removal (E-25): suspending an active
// node succeeds, repeating it gives 409, a landing role gives 400, and a
// missing node gives 404.
func TestPoolSuspendAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	entry := storage.Node{Name: "entry-x", Token: "tok-ex", Role: "entry", Enabled: true}
	if err := db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	landing := storage.Node{Name: "land-x", Token: "tok-lx", Role: "landing", Enabled: true}
	if err := db.Create(&landing).Error; err != nil {
		t.Fatal(err)
	}

	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(entry.ID), 10)+"/suspend", nil); rec.Code != http.StatusOK {
		t.Fatalf("suspend status = %d body=%s", rec.Code, rec.Body)
	}
	// removal leaves a trace
	var n storage.Node
	db.First(&n, entry.ID)
	if n.PoolState != "suspended" || n.PoolReason != "手动摘除" {
		t.Fatalf("suspended node = %+v", n)
	}
	// repeat removal 409
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(entry.ID), 10)+"/suspend", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-suspend status = %d", rec.Code)
	}
	// landing role 400
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(landing.ID), 10)+"/suspend", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("landing suspend status = %d", rec.Code)
	}
	// missing node 404
	if rec := doJSON(t, r, "POST", "/api/pool/424242/suspend", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing suspend status = %d", rec.Code)
	}
}
