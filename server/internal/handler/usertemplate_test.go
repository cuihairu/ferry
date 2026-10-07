package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestUserTemplateCRUD 覆盖默认模板的读取默认值、保存与校验（P1-5）。
func TestUserTemplateCRUD(t *testing.T) {
	r := newTestRouter(t)

	// 未配置时返回全默认
	rec := doJSON(t, r, "GET", "/api/user-template", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get template: %d %s", rec.Code, rec.Body)
	}
	var tpl map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tpl)
	if tpl["quota_bytes"].(float64) != 0 || tpl["expire_days"].(float64) != 0 || tpl["reset_cycle"] != "none" {
		t.Fatalf("default template mismatch: %v", tpl)
	}

	// 保存并回读
	rec = doJSON(t, r, "PUT", "/api/user-template", map[string]any{
		"quota_bytes": 1073741824, "expire_days": 30, "reset_cycle": "month",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}
	rec = doJSON(t, r, "GET", "/api/user-template", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &tpl)
	if tpl["quota_bytes"].(float64) != 1073741824 || tpl["expire_days"].(float64) != 30 || tpl["reset_cycle"] != "month" {
		t.Fatalf("saved template mismatch: %v", tpl)
	}

	// 校验：负配额、负时长、非法周期
	for _, body := range []map[string]any{
		{"quota_bytes": -1},
		{"expire_days": -5},
		{"reset_cycle": "yearly"},
	} {
		if rec := doJSON(t, r, "PUT", "/api/user-template", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid template %v: %d %s", body, rec.Code, rec.Body)
		}
	}
}

// TestCreateUserWithTemplate 覆盖 use_template 补全缺席字段、显式字段优先、
// 不带 use_template 时模板不生效（P1-5）。
func TestCreateUserWithTemplate(t *testing.T) {
	r := newTestRouter(t)

	rec := doJSON(t, r, "PUT", "/api/user-template", map[string]any{
		"quota_bytes": 100, "expire_days": 30, "reset_cycle": "week",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("put template: %d %s", rec.Code, rec.Body)
	}

	before := time.Now()
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "tpl", "use_template": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with template: %d %s", rec.Code, rec.Body)
	}
	var u map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["quota_bytes"].(float64) != 100 || u["reset_cycle"] != "week" {
		t.Fatalf("template fields not applied: %v", u)
	}
	if u["expires_at"] == nil {
		t.Fatal("template expire_days should set expires_at")
	}
	exp := mustTime(t, u["expires_at"].(string))
	want := before.AddDate(0, 0, 30)
	if diff := exp.Sub(want); diff < -time.Minute || diff > time.Minute {
		t.Fatalf("expires_at = %v, want ~%v", exp, want)
	}

	// 显式字段优先于模板
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{
		"username": "mixed", "use_template": true, "quota_bytes": 5, "reset_cycle": "day",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mixed: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["quota_bytes"].(float64) != 5 || u["reset_cycle"] != "day" {
		t.Fatalf("explicit fields should win: %v", u)
	}

	// 不带 use_template：模板不生效，维持原默认口径
	rec = doJSON(t, r, "POST", "/api/users", map[string]any{"username": "plain"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create plain: %d %s", rec.Code, rec.Body)
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u["quota_bytes"].(float64) != 0 || u["reset_cycle"] != "none" || u["expires_at"] != nil {
		t.Fatalf("template must not leak without use_template: %v", u)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return v
}
