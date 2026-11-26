package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestAccountFlows pins the unified flow view (F-1324): money transactions,
// entitlement grants and distributor ledger rows all surface through one
// typed envelope, merged newest-first; the limit caps the merged stream and
// validates its range.
func TestAccountFlows(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelUserOf(t, r, "flow-buyer", nil)
	var u storage.User
	if err := db.Where("username = ?", "flow-buyer").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, r, "POST", "/api/distributors", map[string]any{
		"username": "flowdist", "password": "x12345", "discount_percent": 20,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("create distributor: %d %s", rec.Code, rec.Body)
	}
	var dist storage.Distributor
	if err := db.Where("username = ?", "flowdist").First(&dist).Error; err != nil {
		t.Fatal(err)
	}

	base := time.Now().Add(-time.Hour)
	order := storage.PaymentOrder{OrderNo: "flow-o1", UserID: u.ID, Provider: "epusdt",
		Status: "paid", AmountCents: 1000, CreatedAt: base}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	// One row per ledger, spaced minutes apart so the merge order is total.
	rows := []struct {
		at   time.Time
		seed func(at time.Time) error
	}{
		{base.Add(2 * time.Minute), func(at time.Time) error {
			return db.Create(&storage.PaymentTransaction{OrderNo: "flow-o1", Provider: "epusdt",
				ExternalID: "flow-tx", AmountCents: 1000, Direction: "in", OccurredAt: at, CreatedAt: at}).Error
		}},
		{base.Add(4 * time.Minute), func(at time.Time) error {
			return db.Create(&storage.Grant{OrderNo: "flow-o1", UserID: u.ID,
				GrantType: "add_quota", GrantValue: 50 << 30, CreatedAt: at}).Error
		}},
		{base.Add(6 * time.Minute), func(at time.Time) error {
			return db.Create(&storage.DistributorLedger{DistributorID: dist.ID, OrderNo: "flow-o1",
				Kind: "commission", AmountCents: 200, Note: "佣金", CreatedAt: at}).Error
		}},
	}
	for _, row := range rows {
		if err := row.seed(row.at); err != nil {
			t.Fatal(err)
		}
	}

	rec := doJSON(t, r, "GET", "/api/payments/flows", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("flows: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Flows []struct {
			Kind        string `json:"kind"`
			OrderNo     string `json:"order_no"`
			Ref         string `json:"ref"`
			UserID      uint   `json:"user_id"`
			AmountCents int64  `json:"amount_cents"`
			At          string `json:"at"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 3 {
		t.Fatalf("flows = %d, want 3: %+v", len(out.Flows), out.Flows)
	}
	// Newest first: the commission (6m) then grant (4m) then transaction (2m).
	if out.Flows[0].Kind != "commission" || out.Flows[1].Kind != "grant" || out.Flows[2].Kind != "transaction" {
		t.Fatalf("merge order: %+v", out.Flows)
	}
	if out.Flows[0].AmountCents != 200 || out.Flows[0].Ref != fmt.Sprintf("distributor/%d", dist.ID) {
		t.Fatalf("commission flow: %+v", out.Flows[0])
	}
	if out.Flows[1].UserID != u.ID || out.Flows[1].Ref != "add_quota:51200MiB" {
		t.Fatalf("grant flow: %+v", out.Flows[1])
	}
	if out.Flows[2].OrderNo != "flow-o1" || out.Flows[2].UserID != u.ID ||
		out.Flows[2].AmountCents != 1000 || out.Flows[2].Ref != "epusdt/flow-tx" {
		t.Fatalf("transaction flow: %+v", out.Flows[2])
	}

	// limit=1 caps the merged stream to the single newest flow.
	rec = doJSON(t, r, "GET", "/api/payments/flows?limit=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("flows limit: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Flows) != 1 || out.Flows[0].Kind != "commission" {
		t.Fatalf("limit=1: %+v", out.Flows)
	}
	// Validation: 0 and 501 both reject.
	if rec := doJSON(t, r, "GET", "/api/payments/flows?limit=0", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=0: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/payments/flows?limit=501", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=501: %d", rec.Code)
	}
}
