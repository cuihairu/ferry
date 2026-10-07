package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// subEntries 取回订阅体并解码 base64（v2ray 格式），便于断言入口列表。
func subEntries(t *testing.T, body string) string {
	t.Helper()
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		t.Fatalf("decode sub body: %v", err)
	}
	return string(dec)
}

// TestPoolAPI 覆盖入口池接口（E-16）：池列表只含 entry/both 且带最新探测
// 结论；手动复位仅摘除态可用（active 409、不存在 404）；
// 摘除节点从订阅入口列表消失，复位后回来。
func TestPoolAPI(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 订阅用户 + 两个入口（in-pool / suspended）+ 一个落地（不进池）
	if rec := doJSON(t, r, "POST", "/api/users", map[string]any{"username": "alice", "quota_bytes": 1000}); rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "alice").First(&u)

	nodes := []storage.Node{
		{Name: "hk-entry", Address: "hk.example.com", Port: 443, Protocol: "vless", Token: "t-hk", Role: "entry", Enabled: true, Config: `{"uuid":"u"}`},
		{Name: "us-entry", Address: "us.example.com", Port: 443, Protocol: "vless", Token: "t-us", Role: "entry", Enabled: true, Config: `{"uuid":"u"}`},
		{Name: "jp-landing", Address: "jp.example.com", Port: 443, Protocol: "vless", Token: "t-jp", Role: "landing", Enabled: true},
	}
	for i := range nodes {
		if err := db.Create(&nodes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	target := nodes[1].ID
	if err := db.Create(&storage.ProbeReport{
		NodeID: nodes[0].ID, TargetKind: agentproto.ProbeTargetPeer, TargetNodeID: &target,
		Verdict: agentproto.ProbeVerdictSick, ProbedAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	// ---- 池列表：不含 landing，us-entry 带最新 sick 结论 ----
	rec := doJSON(t, r, "GET", "/api/pool", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pool: %d %s", rec.Code, rec.Body)
	}
	var views []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("pool rows = %d, want 2 (landing excluded)", len(views))
	}
	byID := map[string]map[string]any{}
	for _, v := range views {
		byID[v["name"].(string)] = v
	}
	if v, ok := byID["us-entry"]; !ok || v["last_verdict"] != "sick" || v["pool_state"] != "active" {
		t.Fatalf("us-entry row = %+v", v)
	}
	if v, ok := byID["hk-entry"]; !ok || v["last_verdict"] != "" {
		t.Fatalf("hk-entry row = %+v", v)
	}

	// ---- 订阅：两个入口都在 ----
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("sub: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("sub should list us-entry before suspend: %s", rec.Body.String())
	}

	// ---- active 态手动复位 → 409 ----
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(target), 10)+"/resume", nil); rec.Code != http.StatusConflict {
		t.Fatalf("resume active should 409, got %d %s", rec.Code, rec.Body)
	}

	// ---- 摘除 us-entry → 订阅消失 ----
	if err := db.Model(&storage.Node{}).Where("id = ?", target).
		Updates(map[string]any{"pool_state": pool.StateSuspended, "pool_reason": "连续 3 次探测 sick"}).Error; err != nil {
		t.Fatal(err)
	}
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("suspended entry must be dropped from subscription: %s", rec.Body.String())
	}

	// ---- 手动复位 → 订阅恢复；不存在 404 ----
	if rec := doJSON(t, r, "POST", "/api/pool/"+strconv.FormatUint(uint64(target), 10)+"/resume", nil); rec.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", rec.Code, rec.Body)
	}
	var after storage.Node
	db.First(&after, target)
	if after.PoolState != pool.StateActive || after.PoolReason != "手动复位" {
		t.Fatalf("after resume = %+v", after)
	}
	rec = getSub(t, r, "/sub/"+u.SubToken, "v2rayN/1.0")
	if !strings.Contains(subEntries(t, rec.Body.String()), "us.example.com") {
		t.Fatalf("resumed entry must be back in subscription: %s", rec.Body.String())
	}
	if rec := doJSON(t, r, "POST", "/api/pool/424242/resume", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resume missing should 404: %d", rec.Code)
	}
}
