package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestLoadAPIEdges 覆盖容量口径（实测校准 > 套餐下行 > 上行 > 100 兜底）
// 与单样本差分不可信（util_pct=-1）。
func TestLoadAPIEdges(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	measured := storage.Node{Name: "m", Token: "tok-m", Role: "landing", Enabled: true}
	up := storage.Node{Name: "u", Token: "tok-u", Role: "landing", BwUpMbps: 200, Enabled: true}
	fallback := storage.Node{Name: "f", Token: "tok-f", Role: "landing", Enabled: true}
	for _, n := range []*storage.Node{&measured, &up, &fallback} {
		if err := db.Create(n).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&storage.Node{}).Where("id=?", measured.ID).
		Update("speed_measured_mbps", 300).Error; err != nil {
		t.Fatal(err)
	}
	// 只有一条采样：差分不可信 → 利用率 -1。
	if err := db.Create(&storage.NodeTrafficLog{NodeID: measured.ID, Proc: "relay",
		RxBytes: 1e6, RecordedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, r, "GET", "/api/load", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("load: %d %s", rec.Code, rec.Body)
	}
	var out []struct {
		NodeID       uint `json:"node_id"`
		CapacityMbps int  `json:"capacity_mbps"`
		UtilPct      int  `json:"util_pct"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	capByID := map[uint]int{}
	utilByID := map[uint]int{}
	for _, row := range out {
		capByID[row.NodeID] = row.CapacityMbps
		utilByID[row.NodeID] = row.UtilPct
	}
	if capByID[measured.ID] != 300 || capByID[up.ID] != 200 || capByID[fallback.ID] != 100 {
		t.Fatalf("capacity = %v, want 300/200/100", capByID)
	}
	if utilByID[measured.ID] != -1 {
		t.Fatalf("single sample util = %d, want -1", utilByID[measured.ID])
	}
}

// TestLoadAPIDBErrors 覆盖节点表与流量采样表取数失败 500。
func TestLoadAPIDBErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	regQueryFail("t:loadnodes", db, (*[]storage.Node)(nil))
	if rec := doJSON(t, r, "GET", "/api/load", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("nodes fail: %d %s", rec.Code, rec.Body)
	}

	r2, db2 := newTestRouterWithDB(t)
	regQueryFail("t:loadlogs", db2, (*[]storage.NodeTrafficLog)(nil))
	if rec := doJSON(t, r2, "GET", "/api/load", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("logs fail: %d %s", rec.Code, rec.Body)
	}
}
