package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCardBatchLifecycle(t *testing.T) {
	r := newTestRouter(t)

	// 建批次：3 张 add_quota 卡
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

	// 列表：remaining = total
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

	// 卡密列表
	if rec := doJSON(t, r, "GET", "/api/card-batches/1/codes", nil); rec.Code != http.StatusOK {
		t.Fatalf("codes: %d", rec.Code)
	}

	// CSV 导出：表头 + 3 行 + 附件头
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
