package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestCardBatchLifecycle(t *testing.T) {
	r := newTestRouter(t)

	// Create a batch: 3 add_quota cards
	rec := doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "50GB 卡", "grant_type": "add_quota", "grant_value": 53687091200, "total": 3,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Batch struct {
			ID        uint   `json:"id"`
			Name      string `json:"name"`
			CreatedBy string `json:"created_by"`
		} `json:"batch"`
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.Codes) != 3 {
		t.Fatalf("codes = %d", len(created.Codes))
	}
	for _, code := range created.Codes {
		if !strings.Contains(code, "-") || len(code) != 14 {
			t.Fatalf("bad code format: %q", code)
		}
	}
	if created.Batch.CreatedBy != "admin" {
		t.Fatalf("default created_by = %q", created.Batch.CreatedBy)
	}

	// Listing: remaining = total
	rec = doJSON(t, r, "GET", "/api/card-batches", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d", rec.Code)
	}
	var list []struct {
		ID        uint  `json:"id"`
		Total     int   `json:"total"`
		Remaining int64 `json:"remaining"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].Remaining != 3 {
		t.Fatalf("list = %+v", list)
	}

	// Card code listing
	if rec := doJSON(t, r, "GET", "/api/card-batches/1/codes", nil); rec.Code != http.StatusOK {
		t.Fatalf("codes: %d", rec.Code)
	}

	// CSV export: header + 3 rows + attachment header
	rec = doJSON(t, r, "GET", "/api/card-batches/1/export.csv", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("content-disposition = %q", cd)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "code,status\n") || strings.Count(body, "\n") != 4 {
		t.Fatalf("csv body:\n%s", body)
	}
	if !strings.Contains(body, created.Codes[0]) {
		t.Fatalf("csv missing codes:\n%s", body)
	}

	// 404
	if rec := doJSON(t, r, "GET", "/api/card-batches/99/codes", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing batch: %d", rec.Code)
	}

	// Delete batch: codes go with it, the batch list empties, and the detail returns 404
	if rec := doJSON(t, r, "DELETE", "/api/card-batches/1", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete batch: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/card-batches/1/codes", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("codes after delete: %d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/card-batches", nil)
	var after []any
	_ = json.Unmarshal(rec.Body.Bytes(), &after)
	if len(after) != 0 {
		t.Fatalf("batches after delete = %v", after)
	}
}

// TestCardCodeDisable covers manual disabling: an unused code can be disabled and can no longer be redeemed once disabled; disabling again is 409.
func TestCardCodeDisable(t *testing.T) {
	r := newTestRouter(t)
	rec := doJSON(t, r, "POST", "/api/card-batches", map[string]any{
		"name": "回收测试", "grant_type": "add_quota", "grant_value": 1 << 30, "total": 2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rec.Code, rec.Body)
	}

	rec = doJSON(t, r, "GET", "/api/card-batches/1/codes", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("codes: %d", rec.Code)
	}
	var codes []struct {
		ID     uint   `json:"id"`
		Code   string `json:"code"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &codes); err != nil || len(codes) != 2 {
		t.Fatalf("decode codes: %v %s", err, rec.Body)
	}

	// Disable the first one: 200 + status=disabled
	rec = doJSON(t, r, "PATCH", "/api/card-codes/"+strconv.FormatUint(uint64(codes[0].ID), 10)+"/disable", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	var disabled struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &disabled)
	if disabled.Status != "disabled" {
		t.Fatalf("status = %q", disabled.Status)
	}

	// Redeeming a disabled code fails, and its wording matches the "not found" one (anti-enumeration)
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{
		"code": codes[0].Code, "user_id": 1,
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "兑换失败") {
		t.Fatalf("redeem disabled: %d %s", rec.Code, rec.Body)
	}
	// The second code slot (a non-existent id) returns the same wording
	rec = doJSON(t, r, "POST", "/api/redeem", map[string]any{
		"code": "ZZZZ-ZZZZ-ZZZZ", "user_id": 1,
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "兑换失败") {
		t.Fatalf("redeem unknown: %d %s", rec.Code, rec.Body)
	}

	// Re-disabling is 409; an invalid id is 404
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/"+strconv.FormatUint(uint64(codes[0].ID), 10)+"/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-disable: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/999/disable", nil); rec.Code != http.StatusConflict {
		t.Fatalf("missing code: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "PATCH", "/api/card-codes/abc/disable", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}
}

func TestCardBatchValidation(t *testing.T) {
	r := newTestRouter(t)
	cases := []map[string]any{
		{"name": "", "grant_type": "add_quota", "grant_value": 1, "total": 1},
		{"name": "x", "grant_type": "money", "grant_value": 1, "total": 1},
		{"name": "x", "grant_type": "add_quota", "grant_value": 0, "total": 1},
		{"name": "x", "grant_type": "add_quota", "grant_value": 1, "total": 0},
		{"name": "x", "grant_type": "add_quota", "grant_value": 1, "total": 10001},
		{"name": "x", "grant_type": "extend_days", "grant_value": 4000, "total": 1},
	}
	for i, body := range cases {
		if rec := doJSON(t, r, "POST", "/api/card-batches", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d should 400: %d %s", i, rec.Code, rec.Body)
		}
	}
}
