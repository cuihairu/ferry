package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/storage"
)

const refTestTable = `[
 {"provider":"vultr","region":"tokyo","spec":"100m-500g","monthly_cents":600,"url":"https://example.com/vultr-tokyo"},
 {"provider":"dmit","region":"hk","spec":"100m-500g","monthly_cents":1400}
]`

// TestCostRefTableAndProbe covers E-31: table import validation, single-point probe
// (free-form keys and node mapping), deviation hints and each rejection path.
func TestCostRefTableAndProbe(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Price table not imported: probe → 400 explicitly pointing to the import endpoint.
	rec := doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ref-table") {
		t.Fatalf("no table probe status=%d body=%s", rec.Code, rec.Body)
	}

	// Dirty table rejected: missing monthly price.
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": `[{"provider":"vultr","spec":"x"}]`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad table status=%d body=%s", rec.Code, rec.Body)
	}
	// Non-JSON rejected.
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": `not json`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-json table status=%d", rec.Code)
	}

	// A good table is persisted, leaving a trace in settings.
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable})
	if rec.Code != http.StatusOK {
		t.Fatalf("put table status=%d body=%s", rec.Code, rec.Body)
	}
	if v, ok, _ := storage.GetSetting(db, cost.SettingRefTable); !ok || !strings.Contains(v, "monthly_cents") {
		t.Fatalf("setting missing: ok=%v", ok)
	}

	// After import: non-numeric node_id → 400; both node_id and free-form keys missing → 400.
	if rec := doJSON(t, r, "GET", "/api/cost/ref?node_id=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad node_id: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/cost/ref", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing keys: %d", rec.Code)
	}

	// Free-form key probe hits with no manual price → hit + reason (explanation of no deviation).
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=VULTR&region=Tokyo&spec=100M-500G", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("free probe status=%d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Source    string          `json:"source"`
		Hit       bool            `json:"hit"`
		Quote     map[string]any  `json:"quote"`
		Deviation *map[string]any `json:"deviation"`
		Reason    string          `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Hit || out.Source != "pricelist" {
		t.Fatalf("free probe out=%+v body=%s", out, rec.Body)
	}
	if mc, _ := out.Quote["monthly_cents"].(float64); mc != 600 {
		t.Fatalf("free probe quote=%+v", out.Quote)
	}
	if out.Deviation != nil || out.Reason == "" {
		t.Fatalf("expect reason without manual: %+v", out)
	}

	// Manual price + currency → deviation hint (900 vs 600 = +50%, over the default 30).
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&region=tokyo&spec=100m-500g&manual_cents=900&currency=CNY", nil)
	out = struct {
		Source    string          `json:"source"`
		Hit       bool            `json:"hit"`
		Quote     map[string]any  `json:"quote"`
		Deviation *map[string]any `json:"deviation"`
		Reason    string          `json:"reason"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deviation == nil {
		t.Fatalf("expect deviation: %+v", out)
	}
	if (*out.Deviation)["pct"].(float64) != 50 || (*out.Deviation)["off"] != true {
		t.Fatalf("deviation = %+v", *out.Deviation)
	}

	// Currency mismatch → reason.
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&region=tokyo&spec=100m-500g&manual_cents=900&currency=USD", nil)
	if !strings.Contains(rec.Body.String(), "currency mismatch") {
		t.Fatalf("expect mismatch reason: %s", rec.Body)
	}
	// Manual price without currency → 400.
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m-500g&manual_cents=900", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("manual without currency status=%d", rec.Code)
	}
	// Miss → hit=false.
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&region=la&spec=100m-500g", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hit":false`) {
		t.Fatalf("miss status=%d body=%s", rec.Code, rec.Body)
	}

	// Node-mapped probe: datacenter/region/spec tier all present + manual monthly fixed cost.
	if err := db.Exec(`INSERT INTO nodes (name, address, port, protocol, token, datacenter, region,
		bw_down_mbps, monthly_traffic_quota_bytes, monthly_cost_cents, currency, enabled)
		VALUES ('tok-1', 'tok.example.com', 443, 'vless', 'tok-ref-1', 'vultr', 'tokyo',
		100, 500000000000, 900, 'CNY', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref?node_id=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("node probe status=%d body=%s", rec.Code, rec.Body)
	}
	out = struct {
		Source    string          `json:"source"`
		Hit       bool            `json:"hit"`
		Quote     map[string]any  `json:"quote"`
		Deviation *map[string]any `json:"deviation"`
		Reason    string          `json:"reason"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Hit || out.Deviation == nil || (*out.Deviation)["pct"].(float64) != 50 {
		t.Fatalf("node probe out=%+v", out)
	}
	if q, _ := out.Quote["url"].(string); q == "" {
		t.Fatalf("quote url missing: %+v", out.Quote)
	}

	// Node with no datacenter filled in → 400 guiding to fill the datacenter.
	if err := db.Exec(`INSERT INTO nodes (name, address, port, protocol, token, region,
		monthly_cost_cents, enabled)
		VALUES ('nodc-1', 'x.example.com', 443, 'vless', 'tok-ref-2', 'tokyo', 900, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref?node_id=2", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "datacenter") {
		t.Fatalf("no datacenter status=%d body=%s", rec.Code, rec.Body)
	}
	// Nonexistent node → 404.
	rec = doJSON(t, r, "GET", "/api/cost/ref?node_id=999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node status=%d", rec.Code)
	}

	// After clearing to disable, querying → 400.
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=x", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("after clear status=%d", rec.Code)
	}
}

// TestCostRefCheck covers batch reconciliation: hit and comparable rows enter the list
// (full deviation fields), misses only count, and rows lacking datacenter/monthly fixed cost are not checked.
func TestCostRefCheck(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Price table not imported: reconciliation → 400 explicitly pointing to the import endpoint.
	if rec := doJSON(t, r, "GET", "/api/cost/ref-check", nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ref-table") {
		t.Fatalf("no table check status=%d body=%s", rec.Code, rec.Body)
	}

	rec := doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable})
	if rec.Code != http.StatusOK {
		t.Fatalf("put table status=%d", rec.Code)
	}

	// n1 hits with a deviation (900 vs 1400 = -36%); n2 misses; n3 has no datacenter so is not checked.
	seeds := []string{
		`INSERT INTO nodes (name, address, port, protocol, token, datacenter, region,
			bw_down_mbps, monthly_traffic_quota_bytes, monthly_cost_cents, currency, enabled)
			VALUES ('hk-1', 'hk.example.com', 443, 'vless', 'chk-1', 'dmit', 'hk',
			100, 500000000000, 900, 'CNY', 1)`,
		`INSERT INTO nodes (name, address, port, protocol, token, datacenter, region,
			bw_down_mbps, monthly_traffic_quota_bytes, monthly_cost_cents, currency, enabled)
			VALUES ('la-1', 'la.example.com', 443, 'vless', 'chk-2', 'vultr', 'la',
			100, 500000000000, 900, 'CNY', 1)`,
		`INSERT INTO nodes (name, address, port, protocol, token, region,
			bw_down_mbps, monthly_traffic_quota_bytes, monthly_cost_cents, enabled)
			VALUES ('nodc-2', 'y.example.com', 443, 'vless', 'chk-3', 'hk',
			100, 500000000000, 900, 1)`,
	}
	for _, s := range seeds {
		if err := db.Exec(s).Error; err != nil {
			t.Fatal(err)
		}
	}

	rec = doJSON(t, r, "GET", "/api/cost/ref-check", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ref-check status=%d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Source     string `json:"source"`
		Tolerance  int64  `json:"tolerance_pct"`
		Checked    int    `json:"checked"`
		Hits       int    `json:"hits"`
		Mismatches int    `json:"mismatches"`
		Items      []struct {
			NodeID      uint   `json:"node_id"`
			NodeName    string `json:"node_name"`
			ManualCents int64  `json:"manual_cents"`
			Deviation   struct {
				Pct int64 `json:"pct"`
				Off bool  `json:"off"`
			} `json:"deviation"`
			Quote struct {
				URL string `json:"url"`
			} `json:"quote"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Checked != 2 || out.Hits != 1 || out.Tolerance != 30 {
		t.Fatalf("counters=%+v", out)
	}
	if len(out.Items) != 1 || out.Items[0].NodeName != "hk-1" ||
		out.Items[0].Deviation.Pct != -35 || !out.Items[0].Deviation.Off {
		t.Fatalf("items=%+v", out.Items)
	}

	// tolerance_pct parsing and clamping: an invalid value falls back to the default 30, out-of-range values clamp into 1..200.
	for _, tc := range []struct {
		q    string
		want int64
	}{
		{"tolerance_pct=50", 50},
		{"tolerance_pct=0", 1},
		{"tolerance_pct=500", 200},
		{"tolerance_pct=abc", 30},
	} {
		rec = doJSON(t, r, "GET", "/api/cost/ref-check?"+tc.q, nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Tolerance != tc.want {
			t.Fatalf("%s tolerance=%d, want %d", tc.q, out.Tolerance, tc.want)
		}
	}

	// Currency mismatch row: hits a USD list price but the manual entry is CNY → counts only in mismatches, not listed.
	extended := strings.TrimSuffix(refTestTable, "]") + `,
 {"provider":"vultr","region":"tokyo","spec":"200m-500g","monthly_cents":900,"currency":"USD"}]`
	if rec := doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": extended}); rec.Code != http.StatusOK {
		t.Fatalf("extend table: %d %s", rec.Code, rec.Body)
	}
	if err := db.Exec(`INSERT INTO nodes (name, address, port, protocol, token, datacenter, region,
		bw_down_mbps, monthly_traffic_quota_bytes, monthly_cost_cents, currency, enabled)
		VALUES ('tky-1', 'tky.example.com', 443, 'vless', 'chk-4', 'vultr', 'tokyo',
		200, 500000000000, 900, 'CNY', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref-check", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Checked != 3 || out.Hits != 1 || out.Mismatches != 1 {
		t.Fatalf("mismatch counters=%+v", out)
	}
}
