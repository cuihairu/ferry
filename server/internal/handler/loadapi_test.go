package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestLoadAPI 覆盖负载快照（E-25）：连接数取各进程最新采样和，
// 吞吐由最近两条采样差分，利用率按容量百分比；无采样 util=-1。
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
	// busy：两条采样差分 → rx 增 400MB 在 60s → 53.3Mbps；连接数取最新 12。
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
	// idle：无采样 util=-1。
	if byID[idle.ID].UtilPct != -1 {
		t.Fatalf("idle util = %d", byID[idle.ID].UtilPct)
	}
}

// TestPoolSuspendAPI 覆盖手动摘除（E-25）：active 摘除成功、重复摘除 409、
// 落地角色 400、不存在 404。
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
	// 摘除落痕
	var n storage.Node
	db.First(&n, entry.ID)
	if n.PoolState != "suspended" || n.PoolReason != "手动摘除" {
		t.Fatalf("suspended node = %+v", n)
	}
	// 重复摘除 409
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(entry.ID), 10)+"/suspend", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-suspend status = %d", rec.Code)
	}
	// 落地角色 400
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(landing.ID), 10)+"/suspend", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("landing suspend status = %d", rec.Code)
	}
	// 不存在 404
	if rec := doJSON(t, r, "POST", "/api/pool/424242/suspend", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing suspend status = %d", rec.Code)
	}
}
