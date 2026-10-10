package handler

import (
	"bytes"
	"net/http"
	"strconv"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// itoaU 用户 ID 字符串化（测试用）。
func itoaU(id uint) string { return strconv.Itoa(int(id)) }

// 用户级带宽限额（P2-3）测试：建/改带限额与校验、模板默认补全、
// panelMe 可见、超上限拒绝。

// TestUserBwLimits 覆盖带宽限额的创建/更新/校验/面板可见。
func TestUserBwLimits(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 创建带限额。
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "bwuser", "bw_up_mbps": 50, "bw_down_mbps": 100,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var u storage.User
	db.Where("username = ?", "bwuser").First(&u)
	if u.BwUpMbps != 50 || u.BwDownMbps != 100 {
		t.Fatalf("persisted = %+v", u)
	}

	// 更新只改上行。
	rec = doJSON(t, r, "PUT", "/api/users/"+itoaU(u.ID), map[string]any{"bw_up_mbps": 20})
	if rec.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	db.Where("username = ?", "bwuser").First(&u)
	if u.BwUpMbps != 20 || u.BwDownMbps != 100 {
		t.Fatalf("after update = %+v", u)
	}

	// 校验面：负值拒绝、上限拒绝、0=不限放行、非数字走 bind 400。
	for name, body := range map[string]map[string]any{
		"negative": {"username": "n1", "bw_up_mbps": -1},
		"too big":  {"username": "n2", "bw_down_mbps": 100001},
	} {
		if rec = doJSON(t, r, "POST", "/api/users", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "n3", "bw_up_mbps": 0}); rec.Code != http.StatusCreated {
		t.Fatalf("zero: %d", rec.Code)
	}
	// 更新侧同样校验。
	if rec = doJSON(t, r, "PUT", "/api/users/"+itoaU(u.ID), map[string]any{"bw_up_mbps": -5}); rec.Code != http.StatusBadRequest {
		t.Fatalf("update negative: %d", rec.Code)
	}

	// panelMe 可见（用户自面带限额，与 quota 同口径）。
	rec = doPanel(t, r, u.SubToken, "GET", "/api/panel/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("panel me: %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"bw_up_mbps":20`)) ||
		!bytes.Contains(rec.Body.Bytes(), []byte(`"bw_down_mbps":100`)) {
		t.Fatalf("panel me body: %s", rec.Body.String())
	}
}

// TestUserTemplateBw 覆盖模板默认带宽限额补全（P2-3）。
func TestUserTemplateBw(t *testing.T) {
	r, _ := newTestRouterWithDB(t)

	// 未配模板：建用户不带限额（0=不限）。
	rec := doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl1", "use_template": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("no template: %d %s", rec.Code, rec.Body)
	}
	// 配模板带限额：use_template=true 补全缺席字段。
	if rec = doJSON(t, r, "PUT", "/api/user-template", map[string]any{
		"quota_bytes": 1073741824, "expire_days": 30, "reset_cycle": "month",
		"bw_up_mbps": 10, "bw_down_mbps": 20,
	}); rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl2", "use_template": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("with template: %d %s", rec.Code, rec.Body)
	}
	// 模板配额套上（跨字段验证 applyUserTemplate 生效）。
	if rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "tpl3", "use_template": true, "bw_up_mbps": 99,
	}); rec.Code != http.StatusCreated {
		t.Fatalf("explicit override: %d %s", rec.Code, rec.Body)
	}
}
