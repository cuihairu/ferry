package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestQuotaLinkAPI 覆盖管理端三态：默认读、设置回读、非法配置拒绝。
func TestQuotaLinkAPI(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 默认：关、流量档 90%
	rec := doJSON(t, r, "GET", "/api/quota-link", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get default: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Setting quota.LinkSetting `json:"setting"`
		Rows    []storage.QuotaAction `json:"rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Setting.Enabled || out.Setting.TrafficPercent != quota.DefaultTrafficPercent {
		t.Fatalf("default setting = %+v", out.Setting)
	}

	// 设置回读
	in := quota.LinkSetting{Enabled: true, TrafficPercent: 95, CostCents: 2000, MaxPriceCents: 500}
	if rec := doJSON(t, r, "PUT", "/api/quota-link", in); rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/quota-link", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Setting != in {
		t.Fatalf("roundtrip = %+v, want %+v", out.Setting, in)
	}

	// 启用但零触发阈值：400
	if rec := doJSON(t, r, "PUT", "/api/quota-link", quota.LinkSetting{Enabled: true}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no-trigger enable should 400: %d %s", rec.Code, rec.Body)
	}
}

// TestSubscriptionDowngrade 是 SAVE-6 的订阅出口语义：生效中的联动行让该
// 用户订阅只出低成本档入口（快照档线），释放后恢复完整列表；他人不受影响。
func TestSubscriptionDowngrade(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "downgraded", "quota_bytes": 1000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	uid := uint(u["id"].(float64))
	tok := u["sub_token"].(string)

	rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "bystander", "quota_bytes": 1000, "expires_at": "2030-01-01T00:00:00Z",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bystander: %d", rec.Code)
	}
	var u2 map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u2)
	bystanderTok := u2["sub_token"].(string)

	// 两个入口：cheap 包月（低成本档），pricey 按流量 800 分/GB（超快照档线 500）
	mkNode := func(name, billing, token string) {
		t.Helper()
		rec := doJSON(t, r, "POST", "/api/nodes", map[string]any{
			"name": name, "address": name + ".example.com", "port": 443, "protocol": "vless",
			"config": `{"uuid":"u-` + name + `"}`,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create node %s: %d %s", name, rec.Code, rec.Body)
		}
		if err := db.Model(&storage.Node{}).Where("name = ?", name).Updates(map[string]any{
			"role": "entry", "meta_init": true, "billing_type": billing,
			"traffic_price_cents": 800, "token": token,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mkNode("cheap", "包月", "tok-cheap")
	mkNode("pricey", "按流量", "tok-pricey")

	subLinks := func(t *testing.T, subToken string) []string {
		t.Helper()
		rec := getSub(t, r, "/sub/"+subToken, "v2rayNG/1.8")
		if rec.Code != http.StatusOK {
			t.Fatalf("sub: %d %s", rec.Code, rec.Body)
		}
		raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
		if err != nil {
			t.Fatalf("not base64: %v", err)
		}
		var links []string
		for _, l := range strings.Split(string(raw), "\n") {
			if l != "" {
				links = append(links, l)
			}
		}
		return links
	}

	countHost := func(links []string, host string) int {
		n := 0
		for _, l := range links {
			if strings.Contains(l, host) {
				n++
			}
		}
		return n
	}

	// 无联动：完整列表
	if links := subLinks(t, tok); countHost(links, "pricey.example.com") != 1 || countHost(links, "cheap.example.com") != 1 {
		t.Fatalf("full list expected, got %v", links)
	}

	// 生效中联动行（快照档线 500 分/GB）：只出 cheap
	now := time.Now()
	if err := db.Create(&storage.QuotaAction{
		UserID: uid, Trigger: quota.LinkTriggerCost,
		UsedBytes: 5 << 30, CostCents: 4000, MaxPriceCents: 500,
		Reason: "费用达阈值 ¥40.00", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	links := subLinks(t, tok)
	if countHost(links, "pricey.example.com") != 0 || countHost(links, "cheap.example.com") != 1 {
		t.Fatalf("downgraded list should only have cheap, got %v", links)
	}
	// 旁人完整列表不受影响
	if links := subLinks(t, bystanderTok); countHost(links, "pricey.example.com") != 1 {
		t.Fatalf("bystander should keep full list, got %v", links)
	}

	// 释放 → 恢复完整列表
	if err := db.Model(&storage.QuotaAction{}).Where("user_id = ?", uid).
		Updates(map[string]any{"released_at": now.Add(time.Minute), "release_reason": "阈值回落"}).Error; err != nil {
		t.Fatal(err)
	}
	if links := subLinks(t, tok); countHost(links, "pricey.example.com") != 1 {
		t.Fatalf("released list should restore pricey, got %v", links)
	}
}
