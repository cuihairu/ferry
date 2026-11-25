package handler

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestTrafficRecordErrors covers traffic accounting's malformed payload, missing
// required field, missing reference, and read/write failure branches.
func TestTrafficRecordErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doRaw(t, r, "POST", "/api/traffic-logs", "{bad"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d %s", rec.Code, rec.Body)
	}
	// Row missing user_id → 400 (the accounting primary key cannot be omitted).
	if rec := doJSON(t, r, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"tx_bytes": 1}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing user_id: %d %s", rec.Code, rec.Body)
	}

	// Partially missing reference: one valid node_id + one missing → 400 (the missing
	// value is reported; a non-empty hit set takes the have-intersection comparison).
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

	// Reference id validation read failure → 400 (checkIDsExist's not-found and DB
	// failure share the "missing reference" 400 policy, with the DB error text passed
	// through verbatim; observation item, product behavior unchanged).
	r3, db3 := newTestRouterWithDB(t)
	_, uid3 := createLimitedUser(t, r3, "tl-plk", 0, 0)
	regQueryFail("t:trafficpluck", db3, (*[]uint)(nil))
	if rec := doJSON(t, r3, "POST", "/api/traffic-logs", map[string]any{
		"items": []map[string]any{{"user_id": uint(uid3), "tx_bytes": 1}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("pluck fail: %d %s", rec.Code, rec.Body)
	}

	// Write failure → 500 (batch Create destination is *[]TrafficLog).
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

// TestTrafficReadErrors covers the single-user usage summary's user lookup and
// detail read failure branches.
func TestTrafficReadErrors(t *testing.T) {
	// User does not exist → 404 (findUser's not-found and invalid id share the same policy).
	r, _ := newTestRouterWithDB(t)
	if rec := doJSON(t, r, "GET", "/api/users/999/traffic", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing user: %d", rec.Code)
	}

	// Detail read failure → 500.
	r2, db2 := newTestRouterWithDB(t)
	_, uid := createLimitedUser(t, r2, "tl-rd", 0, 0)
	regQueryFail("t:trafficrows", db2, (*[]storage.TrafficLog)(nil))
	if rec := doJSON(t, r2, "GET", "/api/users/"+strconv.Itoa(int(uid))+"/traffic", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("rows fail: %d %s", rec.Code, rec.Body)
	}
}
