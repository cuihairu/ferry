package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

func TestRoutingConfigRender(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mk := func(tmpl string) uint {
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": "hk-entry", "address": "hk.example.com", "port": 443,
			"protocol": "vless", "config": tmpl,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node: %d %s", rec.Code, rec.Body)
		}
		var n map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &n)
		return uint(n["id"].(float64))
	}

	// 空模板：生成最小骨架（规则 + direct 出站）
	id := mk(`{}`)
	rec := doJSON(t, r, "GET", "/api/nodes/1/routing-config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("render routing-config: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		NodeID uint   `json:"node_id"`
		Sha256 string `json:"sha256"`
		Config string `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.NodeID != id {
		t.Fatalf("node_id = %d, want %d", res.NodeID, id)
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(res.Config), &merged); err != nil {
		t.Fatalf("config not JSON: %v", err)
	}
	rules := merged["routing"].(map[string]any)["rules"].([]any)
	if len(rules) < 2 || rules[0].(map[string]any)["outboundTag"] != "direct" {
		t.Fatalf("rules = %v", rules)
	}
	sum := sha256.Sum256([]byte(res.Config))
	if res.Sha256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 与渲染结果不符")
	}

	// 坏模板：节点 API 本身拒绝非法 JSON，这里经 db 直种走渲染兜底 400
	bad := storage.Node{Name: "bad-tmpl", Address: "b.example.com", Port: 443, Protocol: "vless", Config: "no-json", Enabled: true}
	if err := db.Create(&bad).Error; err != nil {
		t.Fatalf("seed bad node: %v", err)
	}
	rec = doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/routing-config", bad.ID), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad template: %d %s", rec.Code, rec.Body)
	}

	// 不存在的节点：404
	rec = doJSON(t, r, "GET", "/api/nodes/999/routing-config", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node: %d", rec.Code)
	}
}

func TestRuleLibPush(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "hk-entry", "address": "hk.example.com", "port": 443,
		"protocol": "vless", "config": `{}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}

	raw := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// 校验：缺 proc
	if rec := raw("POST", "/api/nodes/1/rulelib?name=geoip.dat", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing proc: %d", rec.Code)
	}
	// 校验：路径穿越文件名
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=..%2Fgeoip.dat", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal name: %d", rec.Code)
	}
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat%2Fx", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("path sep name: %d", rec.Code)
	}
	// 校验：空 body
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d", rec.Code)
	}

	// 推送：测试 hub 无 agent 连接 → 502，但版本快照必须已落库
	body := "fake-geoip-dat-bytes"
	rec = raw("POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat", body)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline push: %d %s", rec.Code, rec.Body)
	}

	var rows []storage.NodeConfig
	if err := db.Where("node_id=1 AND kind=?", "rulelib:geoip.dat").Find(&rows).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(rows))
	}
	sum := sha256.Sum256([]byte(body))
	want := hex.EncodeToString(sum[:])
	if rows[0].Version != want || rows[0].Sha256 != want {
		t.Fatalf("snapshot version/sha256 = %s/%s, want %s", rows[0].Version, rows[0].Sha256, want)
	}
	if rows[0].Status != "failed" {
		t.Fatalf("offline push status = %s, want failed", rows[0].Status)
	}

	// 版本史：不含 payload 字段
	rec = doJSON(t, r, "GET", "/api/nodes/1/rulelib", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list rulelib: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "fake-geoip-dat-bytes") {
		t.Fatal("版本史不应回带文件内容")
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0]["kind"] != "rulelib:geoip.dat" {
		t.Fatalf("list = %v", list)
	}

	// 节点不存在：快照不落库，直接 404
	rec = raw("POST", "/api/nodes/999/rulelib?proc=xray&name=geoip.dat", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node push: %d", rec.Code)
	}
	var cnt int64
	db.Model(&storage.NodeConfig{}).Where("node_id=999").Count(&cnt)
	if cnt != 0 {
		t.Fatalf("missing node 不应落快照，got %d", cnt)
	}
}
