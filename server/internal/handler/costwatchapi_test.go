package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPriceWatchAPI 覆盖关注 CRUD：校验、唯一键 409、部分更新、快照查询
// 与删除级联、命中动态装配（最新/上次/涨跌/到位）。
func TestPriceWatchAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 缺 spec 拒绝。
	rec := doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "vultr"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing spec status=%d", rec.Code)
	}
	// 负目标价拒绝。
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "vultr", "spec": "1c", "target_price": -5})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative target status=%d", rec.Code)
	}

	// 创建（含区域与目标价）。
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

	// 重复键 409（唯一索引 idx_price_watch）。
	rec = doJSON(t, r, "POST", "/api/cost/watches", map[string]any{"provider": "VULTR", "region": "tokyo", "spec": "1c"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", rec.Code, rec.Body)
	}

	// 无快照前列表：无动态。
	rec = doJSON(t, r, "GET", "/api/cost/watches", nil)
	var list []watchView
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].LatestCents != nil || list[0].AtTarget {
		t.Fatalf("list before snapshots = %+v", list)
	}

	// 部分更新：目标价改 700 + 停用。
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"target_price": 700, "enabled": false})
	if err := json.Unmarshal(rec.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.TargetPrice != 700 || w.Enabled {
		t.Fatalf("updated = %+v", w)
	}
	// 重新启用。
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-enable status=%d", rec.Code)
	}

	// 快照查询：落两行后按 id DESC，列表动态装配最新 800 / 上次 1000 / 降 20% / 到位（目标 700? 否）。
	db.Create(&storage.PriceSnapshot{WatchID: w.ID, MonthlyCents: 1000, Source: "pricelist", CapturedAt: time.Now().Add(-time.Hour)})
	db.Create(&storage.PriceSnapshot{WatchID: w.ID, MonthlyCents: 800, Source: "pricelist", CapturedAt: time.Now()})
	db.Create(&storage.PriceSnapshot{WatchID: 999, MonthlyCents: 12345, Source: "pricelist", CapturedAt: time.Now()}) // 他键隔离

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
	if w.AtTarget { // 目标 700 未到位
		t.Fatalf("at_target should be false: %+v", w)
	}

	// 删除级联：关注与快照同删。
	rec = doJSON(t, r, "DELETE", "/api/cost/watches/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status=%d", rec.Code)
	}
	var cnt int64
	db.Model(&storage.PriceSnapshot{}).Where("watch_id = ?", 1).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("snapshots after delete = %d", cnt)
	}
	// 删后 404。
	rec = doJSON(t, r, "PUT", "/api/cost/watches/1", map[string]any{"enabled": true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after delete status=%d", rec.Code)
	}
}
