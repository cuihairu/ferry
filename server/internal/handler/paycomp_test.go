package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestCompensateGrants pins the payment-result compensation (F-1307): paid
// orders with a grant spec and no grant leg are re-granted exactly once,
// refunded orders are never selected, already-granted orders are skipped, and
// the report counts what happened. Re-run idempotence: the second pass grants
// nothing.
func TestCompensateGrants(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	panelUserOf(t, r, "comp-buyer", nil)
	var u storage.User
	if err := db.Where("username = ?", "comp-buyer").First(&u).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	orders := []storage.PaymentOrder{
		{OrderNo: "comp-quota", UserID: u.ID, Provider: "epusdt", Status: "paid",
			AmountCents: 1000, GrantType: "add_quota", GrantValue: 50 << 30, CreatedAt: now},
		{OrderNo: "comp-days", UserID: u.ID, Provider: "epusdt", Status: "paid",
			AmountCents: 2000, GrantType: "extend_days", GrantValue: 30, CreatedAt: now},
		{OrderNo: "comp-done", UserID: u.ID, Provider: "epusdt", Status: "paid",
			AmountCents: 3000, GrantType: "add_quota", GrantValue: 10 << 30, CreatedAt: now},
		{OrderNo: "comp-refd", UserID: u.ID, Provider: "epusdt", Status: "refunded",
			AmountCents: 4000, GrantType: "add_quota", GrantValue: 10 << 30, CreatedAt: now},
		{OrderNo: "comp-nogrant", UserID: u.ID, Provider: "epusdt", Status: "paid",
			AmountCents: 5000, CreatedAt: now},
	}
	if err := db.Create(&orders).Error; err != nil {
		t.Fatal(err)
	}
	// comp-done already carries its grant leg.
	if err := db.Create(&storage.Grant{OrderNo: "comp-done", UserID: u.ID,
		GrantType: "add_quota", GrantValue: 10 << 30, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	quotaBefore := u.QuotaBytes

	rec := doJSON(t, r, "POST", "/api/payments/compensate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compensate: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Granted int `json:"granted"`
		Results []struct {
			OrderNo string `json:"order_no"`
			Action  string `json:"action"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Granted != 2 {
		t.Fatalf("granted = %d, want 2: %+v", out.Granted, out.Results)
	}
	byNo := map[string]string{}
	for _, res := range out.Results {
		byNo[res.OrderNo] = res.Action
	}
	// The report covers exactly the flagged orders: unflagged ones
	// (already-granted, refunded, no grant spec) are never selected.
	if len(out.Results) != 2 || byNo["comp-quota"] != "granted" || byNo["comp-days"] != "granted" {
		t.Fatalf("expected exactly the two flagged orders granted: %+v", out.Results)
	}
	if _, seen := byNo["comp-done"]; seen {
		t.Fatalf("already-granted order should not be selected: %+v", byNo)
	}
	if _, seen := byNo["comp-refd"]; seen {
		t.Fatalf("refunded order should never be selected: %+v", byNo)
	}
	if _, seen := byNo["comp-nogrant"]; seen {
		t.Fatalf("no-grant-spec order should not be selected: %+v", byNo)
	}

	// The grant legs really landed: quota raised, expiry pushed out 30 days.
	var after storage.User
	if err := db.First(&after, u.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.QuotaBytes != quotaBefore+50<<30 {
		t.Fatalf("quota %d, want +%d", after.QuotaBytes, 50<<30)
	}
	if after.ExpiresAt == nil || !after.ExpiresAt.After(now.AddDate(0, 0, 28)) {
		t.Fatalf("expiry = %v, want ~+30d from zero value", after.ExpiresAt)
	}
	var grants []storage.Grant
	if err := db.Where("order_no IN ?", []string{"comp-quota", "comp-days"}).Find(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if len(grants) != 2 {
		t.Fatalf("grant rows = %d, want 2", len(grants))
	}

	// Second pass: nothing left to grant.
	rec = doJSON(t, r, "POST", "/api/payments/compensate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("compensate 2: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Granted != 0 {
		t.Fatalf("second pass granted %d, want 0 (idempotent)", out.Granted)
	}
	var total int64
	db.Model(&storage.Grant{}).Where("order_no IN ?", []string{"comp-quota", "comp-days"}).Count(&total)
	if total != 2 {
		t.Fatalf("grant rows after rerun = %d, want 2", total)
	}
	if after2 := func() int64 {
		var x storage.User
		db.First(&x, u.ID)
		return x.QuotaBytes
	}(); after2 != after.QuotaBytes {
		t.Fatalf("quota drifted on rerun: %d -> %d", after.QuotaBytes, after2)
	}
}
