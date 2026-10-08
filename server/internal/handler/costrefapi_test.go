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

// TestCostRefTableAndProbe 覆盖 E-31：表导入校验、单点试查（自由键与
// 节点映射）、偏差提示与各拒绝面。
func TestCostRefTableAndProbe(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 未导入价格表：试查 400 明确指引导入端点。
	rec := doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ref-table") {
		t.Fatalf("no table probe status=%d body=%s", rec.Code, rec.Body)
	}

	// 脏表拒绝：缺月价。
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": `[{"provider":"vultr","spec":"x"}]`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad table status=%d body=%s", rec.Code, rec.Body)
	}
	// 非 JSON 拒绝。
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": `not json`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-json table status=%d", rec.Code)
	}

	// 好表落库，settings 留痕。
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable})
	if rec.Code != http.StatusOK {
		t.Fatalf("put table status=%d body=%s", rec.Code, rec.Body)
	}
	if v, ok, _ := storage.GetSetting(db, cost.SettingRefTable); !ok || !strings.Contains(v, "monthly_cents") {
		t.Fatalf("setting missing: ok=%v", ok)
	}

	// 自由键试查命中、不带手录价 → hit + reason（无偏差说明）。
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

	// 带手录价+币种 → 偏差提示（900 vs 600 = +50%，超缺省 30）。
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

	// 币种不符 → reason。
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&region=tokyo&spec=100m-500g&manual_cents=900&currency=USD", nil)
	if !strings.Contains(rec.Body.String(), "currency mismatch") {
		t.Fatalf("expect mismatch reason: %s", rec.Body)
	}
	// 带手录价缺币种 → 400。
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=100m-500g&manual_cents=900", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("manual without currency status=%d", rec.Code)
	}
	// 未命中 → hit=false。
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&region=la&spec=100m-500g", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hit":false`) {
		t.Fatalf("miss status=%d body=%s", rec.Code, rec.Body)
	}

	// 节点映射试查：机房/区域/套餐档齐全 + 手录月固定成本。
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

	// 机房未填节点 → 400 指引补机房。
	if err := db.Exec(`INSERT INTO nodes (name, address, port, protocol, token, region,
		monthly_cost_cents, enabled)
		VALUES ('nodc-1', 'x.example.com', 443, 'vless', 'tok-ref-2', 'tokyo', 900, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref?node_id=2", nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "datacenter") {
		t.Fatalf("no datacenter status=%d body=%s", rec.Code, rec.Body)
	}
	// 不存在节点 → 404。
	rec = doJSON(t, r, "GET", "/api/cost/ref?node_id=999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node status=%d", rec.Code)
	}

	// 清空停用后再查 → 400。
	rec = doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status=%d", rec.Code)
	}
	rec = doJSON(t, r, "GET", "/api/cost/ref?provider=vultr&spec=x", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("after clear status=%d", rec.Code)
	}
}

// TestCostRefCheck 覆盖批量对账：命中可比的进列表（偏差字段齐），
// 未命中只进计数，机房/月固定成本不齐的不参查。
func TestCostRefCheck(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	rec := doJSON(t, r, "PUT", "/api/cost/ref-table", map[string]any{"table": refTestTable})
	if rec.Code != http.StatusOK {
		t.Fatalf("put table status=%d", rec.Code)
	}

	// n1 命中且偏差（900 vs 1400 = -36%）；n2 未命中；n3 机房缺不参查。
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
		Source    string `json:"source"`
		Tolerance int64  `json:"tolerance_pct"`
		Checked   int    `json:"checked"`
		Hits      int    `json:"hits"`
		Items     []struct {
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
}
