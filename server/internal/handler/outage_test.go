package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestOutage 覆盖断联容灾（TOUCH-7）：断联态开关缺省常态、缺 enabled 400、
// 订阅注释常附备用公告地址与启用域名清单（主备标注，停用不泄）、
// 断联态打开后 v2ray 解码文本与 clash YAML 注释都带警告行、关闭即撤。
func TestOutage(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 缺省常态
	rec := doJSON(t, r, "GET", "/api/outage", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"enabled":false}` {
		t.Fatalf("default outage: %d %s", rec.Code, rec.Body)
	}
	// 缺 enabled 400
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: %d", rec.Code)
	}

	// 备用域名入库：主/备/停用各一（停用不进注释）
	for _, d := range []storage.EntryDomain{
		{Domain: "main.example.com", Role: "primary", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "spare.example.com", Role: "backup", Enabled: true, UpdatedAt: time.Now()},
		{Domain: "dead.example.com", Role: "backup", Enabled: false, UpdatedAt: time.Now()},
	} {
		if err := db.Create(&d).Error; err != nil {
			t.Fatal(err)
		}
	}

	// 订阅用户（无节点也行：备注照附，逃生通道在断联前就得进客户端缓存）
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "outage-user"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	tok := u["sub_token"].(string)

	// 常态：注释带公告地址与域名清单，无警告
	raw, err := base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil {
		t.Fatalf("v2ray body not base64: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "公告订阅") || !strings.Contains(text, "/feed.xml") {
		t.Fatalf("feed note missing: %q", text)
	}
	if !strings.Contains(text, "main.example.com(主)") || !strings.Contains(text, "spare.example.com(备)") {
		t.Fatalf("domain note missing: %q", text)
	}
	if strings.Contains(text, "dead.example.com") || strings.Contains(text, "警告") {
		t.Fatalf("disabled domain leaked or premature warning: %q", text)
	}
	clashBody := getSub(t, r, "/sub/"+tok+"?target=clash", "curl/8.0").Body.String()
	if !strings.Contains(clashBody, "# 公告订阅") || !strings.Contains(clashBody, "# 备用入口域名") {
		t.Fatalf("clash notes missing:\n%s", clashBody)
	}

	// 断联态打开：警告行进两种订阅文本
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{"enabled": true}); rec.Code != http.StatusOK {
		t.Fatalf("put outage: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, r, "GET", "/api/outage", nil); rec.Body.String() != `{"enabled":true}` {
		t.Fatalf("outage not on: %s", rec.Body)
	}
	raw, err = base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil || !strings.Contains(string(raw), "警告：面板入口处于断联态") {
		t.Fatalf("v2ray warning missing: %v %q", err, string(raw))
	}
	clashBody = getSub(t, r, "/sub/"+tok+"?target=clash", "curl/8.0").Body.String()
	if !strings.Contains(clashBody, "# 警告：面板入口处于断联态") {
		t.Fatalf("clash warning missing:\n%s", clashBody)
	}

	// 关闭即撤
	if rec := doJSON(t, r, "PUT", "/api/outage", map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("put outage off: %d", rec.Code)
	}
	raw, err = base64.StdEncoding.DecodeString(getSub(t, r, "/sub/"+tok+"?target=v2ray", "curl/8.0").Body.String())
	if err != nil || strings.Contains(string(raw), "警告") {
		t.Fatalf("warning should clear: %v %q", err, string(raw))
	}
}
