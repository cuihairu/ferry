package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNotifyChannel 覆盖通知渠道管理（P1-10）：查看默认、保存、校验、
// 测试外发端到端、未配置时测试 400、无令牌 401。
func TestNotifyChannel(t *testing.T) {
	r := newTestRouter(t)

	// 默认未启用
	w := doAdmin(t, r, "GET", "/admin/notify", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get notify: %d %s", w.Code, w.Body)
	}
	var st struct {
		URL             string `json:"url"`
		SecretConfigured bool   `json:"secret_configured"`
		Enabled         bool   `json:"enabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Enabled || st.URL != "" || st.SecretConfigured {
		t.Fatalf("default notify state mismatch: %+v", st)
	}

	// 测试外发端到端：接收端用 httptest
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := `{"url":"` + srv.URL + `","secret":"k1"}`
	if w = doAdmin(t, r, "PUT", "/admin/notify", []byte(body)); w.Code != http.StatusOK {
		t.Fatalf("put notify: %d %s", w.Code, w.Body)
	}
	w = doAdmin(t, r, "GET", "/admin/notify", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Enabled || st.URL != srv.URL || !st.SecretConfigured {
		t.Fatalf("saved notify state mismatch: %+v", st)
	}

	// POST /admin/notify/test 真发一条
	if w = doAdmin(t, r, "POST", "/admin/notify/test", nil); w.Code != http.StatusOK {
		t.Fatalf("test notify: %d %s", w.Code, w.Body)
	}
	if hits != 1 {
		t.Fatalf("webhook hits = %d, want 1", hits)
	}

	// 非法 URL 400
	if w = doAdmin(t, r, "PUT", "/admin/notify", []byte(`{"url":"ftp://x"}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("bad url should 400: %d %s", w.Code, w.Body)
	}

	// 未配置时测试 400：清空 url 即停用
	if w = doAdmin(t, r, "PUT", "/admin/notify", []byte(`{"url":""}`)); w.Code != http.StatusOK {
		t.Fatalf("disable notify: %d %s", w.Code, w.Body)
	}
	if w = doAdmin(t, r, "POST", "/admin/notify/test", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("test without webhook should 400: %d %s", w.Code, w.Body)
	}

	// 无令牌 401
	if w := doBare(t, r, "GET", "/admin/notify"); w.Code != http.StatusUnauthorized {
		t.Fatalf("without token should 401: %d", w.Code)
	}
}
