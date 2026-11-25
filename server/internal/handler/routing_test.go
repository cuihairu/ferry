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

	"github.com/cuihairu/ferry/packages/agentproto"
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

	// empty template: minimal skeleton generated (rules + direct outbound)
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

	// bad template: the node API itself rejects invalid JSON, so seed via db directly to hit the render fallback 400
	bad := storage.Node{Name: "bad-tmpl", Address: "b.example.com", Port: 443, Protocol: "vless", Config: "no-json", Enabled: true}
	if err := db.Create(&bad).Error; err != nil {
		t.Fatalf("seed bad node: %v", err)
	}
	rec = doJSON(t, r, "GET", fmt.Sprintf("/api/nodes/%d/routing-config", bad.ID), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad template: %d %s", rec.Code, rec.Body)
	}

	// missing node: 404
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
		// admin data plane (apiAuth): carries the admin JWT like doJSON does.
		req.Header.Set("Authorization", "Bearer "+adminJWT(t))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// validation: missing proc
	if rec := raw("POST", "/api/nodes/1/rulelib?name=geoip.dat", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing proc: %d", rec.Code)
	}
	// validation: path-traversal file name
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=..%2Fgeoip.dat", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal name: %d", rec.Code)
	}
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat%2Fx", "data"); rec.Code != http.StatusBadRequest {
		t.Fatalf("path sep name: %d", rec.Code)
	}
	// validation: empty body
	if rec := raw("POST", "/api/nodes/1/rulelib?proc=xray&name=geoip.dat", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d", rec.Code)
	}

	// push: the test hub has no agent connection -> 502, but the version snapshot must already be persisted
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

	// version history: contains no payload field
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

	// node missing: no snapshot persisted, straight to 404
	rec = raw("POST", "/api/nodes/999/rulelib?proc=xray&name=geoip.dat", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing node push: %d", rec.Code)
	}
	var cnt int64
	db.Model(&storage.NodeConfig{}).Where("node_id=999").Count(&cnt)
	if cnt != 0 {
		t.Fatalf("missing node 不应落快照，got %d", cnt)
	}

	// invalid node id: both push and version history return 404
	if rec := raw("POST", "/api/nodes/abc/rulelib?proc=xray&name=geoip.dat", body); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id push: %d", rec.Code)
	}
	if rec := doJSON(t, r, "GET", "/api/nodes/abc/rulelib", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id list: %d", rec.Code)
	}
	// version history: a valid limit value takes effect
	rec = doJSON(t, r, "GET", "/api/nodes/1/rulelib?limit=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list limit: %d %s", rec.Code, rec.Body)
	}
}

// TestRuleLibPushAck covers the rule lib push ack path: agent replies with an
// OK ack -> snapshot applied and the response carries no file content; an unparseable ack -> 502 with the snapshot failed and the cause recorded.
func TestRuleLibPushAck(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "rl-ack")
	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "rl-ack", token)

	// responder: first reply is an OK ack, the second an unparseable payload (covers the bad-ack branch).
	go func() {
		first := true
		for {
			var env agentproto.Envelope
			if err := c.ReadJSON(&env); err != nil {
				return
			}
			if env.Type != agentproto.MsgConfigPush {
				continue
			}
			var payload any = agentproto.ConfigAck{Proc: "xray", OK: true, Validated: true}
			if !first {
				payload = []byte("junk-not-json")
			}
			first = false
			ack, err := agentproto.NewEnvelope(env.ID, agentproto.MsgConfigAck, payload)
			if err != nil {
				return
			}
			if err := c.WriteJSON(ack); err != nil {
				return
			}
		}
	}()

	raw := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+adminJWT(t))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// success: ack OK -> 200, snapshot applied, and neither the response nor the version history carries file content
	rec := raw(fmt.Sprintf("/api/nodes/%d/rulelib?proc=xray&name=geosite.dat", id), "ack-bytes")
	if rec.Code != http.StatusOK {
		t.Fatalf("ack push: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "ack-bytes") {
		t.Fatal("推送响应不应回带文件内容")
	}
	var row storage.NodeConfig
	if err := db.Where("node_id=? AND kind=?", id, "rulelib:geosite.dat").First(&row).Error; err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if row.Status != "applied" || !row.Validated {
		t.Fatalf("applied snapshot = %+v", row)
	}

	// bad ack: unparseable payload -> 502, snapshot failed with the cause persisted
	rec = raw(fmt.Sprintf("/api/nodes/%d/rulelib?proc=xray&name=geoip.dat", id), "bad-ack-bytes")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("bad ack push: %d %s", rec.Code, rec.Body)
	}
	var badRow storage.NodeConfig
	if err := db.Where("node_id=? AND kind=?", id, "rulelib:geoip.dat").First(&badRow).Error; err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if badRow.Status != "failed" || !strings.Contains(badRow.Error, "bad config_ack") {
		t.Fatalf("bad ack snapshot = %+v", badRow)
	}
}

// TestRoutingAdsToggle covers the ad/tracking blocking toggle (SAVE-4): on by default (render
// includes ad-block rules and the block outbound) -> off, render excludes them ->
// invalid request 400 -> re-enable restores.
func TestRoutingAdsToggle(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
		"name": "ads-1", "address": "ads.example.com", "port": 443,
		"protocol": "vless", "config": `{}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create node: %d %s", rec.Code, rec.Body)
	}

	hasBlock := func() bool {
		t.Helper()
		rec := doJSON(t, r, "GET", "/api/nodes/1/routing-config", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("render routing-config: %d %s", rec.Code, rec.Body)
		}
		var res struct {
			Config string           `json:"config"`
			Sets   []map[string]any `json:"sets"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var merged map[string]any
		if err := json.Unmarshal([]byte(res.Config), &merged); err != nil {
			t.Fatalf("config not JSON: %v", err)
		}
		hasAdSet := false
		for _, s := range res.Sets {
			if s["name"] == "ad-block" {
				hasAdSet = true
			}
		}
		obs, _ := merged["outbounds"].([]any)
		hasBlockOut := false
		for _, ob := range obs {
			if m, ok := ob.(map[string]any); ok && m["tag"] == "block" {
				hasBlockOut = true
			}
		}
		if hasAdSet != hasBlockOut {
			t.Fatalf("ad-block 清单与 block 出站应同进退: sets=%v outbounds=%v", res.Sets, obs)
		}
		return hasAdSet
	}

	// on by default
	if !hasBlock() {
		t.Fatal("ads block should be enabled by default")
	}

	// disable: render no longer contains the blocking rule set or the block outbound
	rec = doJSON(t, r, "PUT", "/api/routing/ads", map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable ads: %d %s", rec.Code, rec.Body)
	}
	if hasBlock() {
		t.Fatal("ads block should be off after disable")
	}
	if v, ok, err := storage.GetSetting(db, adsSettingKey); err != nil || !ok || v != "0" {
		t.Fatalf("ads setting = %q ok=%v err=%v, want \"0\"", v, ok, err)
	}

	// invalid request: missing enabled field -> 400
	if rec := doJSON(t, r, "PUT", "/api/routing/ads", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled should 400: %d", rec.Code)
	}

	// re-enable restores
	rec = doJSON(t, r, "PUT", "/api/routing/ads", map[string]any{"enabled": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable ads: %d %s", rec.Code, rec.Body)
	}
	if !hasBlock() {
		t.Fatal("ads block should be back after re-enable")
	}
}
