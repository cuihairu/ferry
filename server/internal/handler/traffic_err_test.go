package handler

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestTrafficRecordErrors 覆盖流量记账的坏载荷、缺必填、引用缺失、
// 取数与落库失败分支。
func TestTrafficRecordErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, "POST", "/api/traffic-logs", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d %s", rec.Code, rec.Body)
	}
	// 行缺 user_id → 400（记账主键不可省）。
	if rec := doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"tx_bytes": 1}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id: %d %s", rec.Code, rec.Body)
	}

	// 引用部分缺失：一个有效 node_id + 一个缺失 → 400（缺失值回报，
	// 命中集非空走 have 交集比对）。
	r2, _ := newTestRouterWithDB(t)
	_, uid := createLimitedUser(t, r2, "tl-ref", 0, 0)
	nid, _ := createNodeFor(t, r2, "tl-node")
	if rec := doJSON(t, r2, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{
			{"user_id": uint(uid), "node_id": nid, "rx_bytes": 1},
			{"user_id": uint(uid), "node_id": uint(99), "rx_bytes": 1},
		},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown node: %d %s", rec.Code, rec.Body)
	}

	// 引用 id 校验取数失败 → 400（checkIDsExist 的查无与库故障同走
	// 「引用缺失」400 口径，库错文案原样透出；观察项，不改产品行为）。
	r3, db3 := newTestRouterWithDB(t)
	_, uid3 := createLimitedUser(t, r3, "tl-plk", 0, 0)
	regQueryFail("t:trafficpluck", db3, (*[]uint)(nil))
	if rec := doJSON(t, r3, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": uint(uid3), "tx_bytes": 1}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("pluck fail: %d %s", rec.Code, rec.Body)
	}

	// 落库失败 → 500（批量 Create 目标 *[]TrafficLog）。
	r4, db4 := newTestRouterWithDB(t)
	_, uid4 := createLimitedUser(t, r4, "tl-wr", 0, 0)
	db4.Callback().Create().Before("gorm:create").Register("t:trafficcreate", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*[]storage.TrafficLog); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if rec := doJSON(t, r4, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": uint(uid4), "tx_bytes": 1}},
	}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create fail: %d %s", rec.Code, rec.Body)
	}
}

// TestTrafficReadErrors 覆盖单用户用量汇总的用户定位与明细取数失败分支。
func TestTrafficReadErrors(t *testing.T) {
	// 用户不存在 → 404（findUser 查无与非法 id 同口径）。
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/users/999/traffic", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d", rec.Code)
	}

	// 明细取数失败 → 500。
	r2, db2 := newTestRouterWithDB(t)
	_, uid := createLimitedUser(t, r2, "tl-rd", 0, 0)
	regQueryFail("t:trafficrows", db2, (*[]storage.TrafficLog)(nil))
	if rec := doJSON(t, r2, "GET", "/api/users/"+strconv.Itoa(int(uid))+"/traffic", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows fail: %d %s", rec.Code, rec.Body)
	}
}
