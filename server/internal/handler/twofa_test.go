package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// seedAdmin 建一个管理员账号（bcrypt 口径与 AdminLogin 一致）。
func seedAdmin(t *testing.T, db *gorm.DB, name, password string) storage.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u := storage.User{
		Username: name, SubToken: name + "-sub-token", Password: string(hash),
		Enabled: true, IsAdmin: true,
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	return u
}

// adminLogin 以指定 IP/UA 走登录端点（UA 固定供审计断言）。
func adminLogin(t *testing.T, r *gin.Engine, ip, username, password, code string) *httptest.ResponseRecorder {
	t.Helper()
	buf := bytes.NewBufferString(`{"username":"` + username + `","password":"` + password + `"`)
	if code != "" {
		buf.WriteString(`,"totp":"` + code + `"`)
	}
	buf.WriteString("}")
	req := httptest.NewRequest("POST", "/admin/login", buf)
	req.RemoteAddr = ip + ":5678"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ferry-test-ua/1.0")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// doJSONAuth 带管理员令牌发请求（2FA 与审计端点均在鉴权组内）。
func doJSONAuth(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// loginBody 解析登录响应的 token/错误码。
func loginBody(t *testing.T, rec *httptest.ResponseRecorder) (token, code string) {
	t.Helper()
	var out struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode login resp: %v (%s)", err, rec.Body)
	}
	return out.Token, out.Code
}

// totpNow 生成当前时刻的合法 TOTP 码。
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	return code
}

// badTotpCode 挑一个确定非法的 6 位码（按校验参数实测，避开 ±1 周期容差
// 撞上的万分位抖动）。
func badTotpCode(t *testing.T, secret string) string {
	t.Helper()
	for _, cand := range []string{"000000", "111111", "222222", "123456", "654323", "999999"} {
		valid, err := totp.ValidateCustom(cand, secret, time.Now(), twofaOpts())
		if err == nil && valid {
			continue
		}
		return cand
	}
	t.Fatal("no invalid totp code candidate")
	return ""
}

// TestTwoFABindLoginRecovery 覆盖绑定→校验→恢复码→解绑全链路：setup 产出
// 密钥与 otpauth URL，enable 校验 TOTP 后落密文密钥与 8 枚恢复码；绑定后
// 登录须 TOTP 或恢复码（缺码 totp_required、错码 totp_invalid），恢复码
// 用一个销一个；解绑后登录恢复直过。
func TestTwoFABindLoginRecovery(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")

	// 未绑定状态。
	if rec := doJSONAuth(t, r, "GET", "/admin/2fa", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("status unbound: %d %s", rec.Code, rec.Body)
	}

	// setup：密钥与 otpauth URL。
	rec := doJSONAuth(t, r, "POST", "/admin/2fa/setup", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	var setup struct {
		Secret string `json:"secret"`
		URL    string `json:"otpauth_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil || setup.Secret == "" {
		t.Fatalf("setup resp: %v %s", err, rec.Body)
	}
	if !strings.HasPrefix(setup.URL, "otpauth://totp/ferry:root") {
		t.Fatalf("otpauth url = %q", setup.URL)
	}

	// 错码拒绝，正确码完成绑定并回 8 枚恢复码。
	if rec := doJSONAuth(t, r, "POST", "/admin/2fa/enable", map[string]any{"code": badTotpCode(t, setup.Secret)}); rec.Code != http.StatusBadRequest {
		t.Fatalf("enable wrong code: %d %s", rec.Code, rec.Body)
	}
	rec = doJSONAuth(t, r, "POST", "/admin/2fa/enable", map[string]any{"code": totpNow(t, setup.Secret)})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}
	var en struct {
		Codes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &en); err != nil {
		t.Fatalf("decode enable: %v", err)
	}
	if len(en.Codes) != RecoveryCodeCount {
		t.Fatalf("recovery codes = %d, want %d", len(en.Codes), RecoveryCodeCount)
	}
	reFormat := regexp.MustCompile(`^[0-9a-f]{4}-[0-9a-f]{4}$`)
	for _, code := range en.Codes {
		if !reFormat.MatchString(code) {
			t.Fatalf("recovery code %q not in xxxx-xxxx form", code)
		}
	}
	// 密钥密文落库、暂存键清空、恢复码哈希行就位。
	var u storage.User
	db.First(&u, "username = ?", "root")
	if !u.TOTPEnabled || !strings.HasPrefix(u.TOTPSecret, "v1:") {
		t.Fatalf("user twofa: enabled=%v secret=%.20q", u.TOTPEnabled, u.TOTPSecret)
	}
	if _, ok, _ := storage.GetSetting(db, twofaPendingKey); ok {
		t.Fatal("pending secret should be cleared after enable")
	}
	var codeRows int64
	db.Model(&storage.RecoveryCode{}).Where("user_id = ?", u.ID).Count(&codeRows)
	if codeRows != RecoveryCodeCount {
		t.Fatalf("recovery rows = %d, want %d", codeRows, RecoveryCodeCount)
	}

	// 登录二次校验：缺码、错码、正确码、恢复码、恢复码重放。
	if rec := adminLogin(t, r, "192.0.2.10", "root", "pw-root", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing code: %d", rec.Code)
	} else if _, code := loginBody(t, rec); code != "totp_required" {
		t.Fatalf("code = %q, want totp_required", code)
	}
	if rec := adminLogin(t, r, "192.0.2.10", "root", "pw-root", badTotpCode(t, setup.Secret)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", rec.Code)
	} else if _, code := loginBody(t, rec); code != "totp_invalid" {
		t.Fatalf("code = %q, want totp_invalid", code)
	}
	if rec := adminLogin(t, r, "192.0.2.10", "root", "pw-root", totpNow(t, setup.Secret)); rec.Code != http.StatusOK {
		t.Fatalf("valid code: %d %s", rec.Code, rec.Body)
	}
	if rec := adminLogin(t, r, "192.0.2.10", "root", "pw-root", en.Codes[0]); rec.Code != http.StatusOK {
		t.Fatalf("recovery login: %d %s", rec.Code, rec.Body)
	}
	if rec := adminLogin(t, r, "192.0.2.10", "root", "pw-root", en.Codes[0]); rec.Code != http.StatusUnauthorized {
		t.Fatalf("recovery replay should fail: %d", rec.Code)
	}

	// 解绑：密码错拒绝，密码对清密钥与恢复码，登录回到直过。
	if rec := doJSONAuth(t, r, "POST", "/admin/2fa/disable", map[string]any{"password": "bad"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable wrong password: %d", rec.Code)
	}
	if rec := doJSONAuth(t, r, "POST", "/admin/2fa/disable", map[string]any{"password": "pw-root"}); rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	db.First(&u, "username = ?", "root")
	if u.TOTPEnabled || u.TOTPSecret != "" {
		t.Fatalf("after disable: enabled=%v secret=%q", u.TOTPEnabled, u.TOTPSecret)
	}
	var left int64
	db.Model(&storage.RecoveryCode{}).Count(&left)
	if left != 0 {
		t.Fatalf("recovery rows after disable = %d, want 0", left)
	}
	if rec := adminLogin(t, r, "192.0.2.11", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("direct pass after disable: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFASetupNeedsMasterKey 覆盖加密面拒绝：主密钥未配置时绑定明确
// 报错（密钥永不明文落库）。
func TestTwoFASetupNeedsMasterKey(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")
	if rec := doJSONAuth(t, r, "POST", "/admin/2fa/setup", nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "FERRY_SECRET_KEY") {
		t.Fatalf("setup without master key: %d %s", rec.Code, rec.Body)
	}
}

// TestLoginUnboundDirectPass 覆盖未绑定直过：无 2FA 管理员不带 totp 字段
// 登录照常成功，且成功落一行登录审计（IP/UA/结果齐）。
func TestLoginUnboundDirectPass(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")
	rec := adminLogin(t, r, "198.51.100.7", "root", "pw-root", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	if token, _ := loginBody(t, rec); token == "" {
		t.Fatal("no token")
	}
	var row storage.LoginLog
	if err := db.Where("ok = ?", true).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("login log row: %v", err)
	}
	if row.Username != "root" || row.IP != "198.51.100.7" || row.UA != "ferry-test-ua/1.0" {
		t.Fatalf("log row = %+v", row)
	}
}

// TestLoginLogsQuery 覆盖审计落行与分页：失败/成功各落行，管理端分页
// 列表新→旧排序、total 与页容量正确。
func TestLoginLogsQuery(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")
	if rec := adminLogin(t, r, "203.0.113.9", "root", "wrong", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("failed login: %d", rec.Code)
	}
	if rec := adminLogin(t, r, "203.0.113.8", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("success login: %d", rec.Code)
	}
	var all int64
	db.Model(&storage.LoginLog{}).Count(&all)
	if all < 2 {
		t.Fatalf("login rows = %d, want >= 2", all)
	}

	page := func(p int) (total int64, items []storage.LoginLog) {
		req := httptest.NewRequest("GET", "/admin/login-logs?page="+strconv.Itoa(p)+"&page_size=1", nil)
		req.Header.Set("Authorization", "Bearer "+adminJWT(t))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("login-logs: %d %s", w.Code, w.Body)
		}
		var out struct {
			Total int64              `json:"total"`
			Items []storage.LoginLog `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out.Total, out.Items
	}
	total, items := page(1)
	if total != all || len(items) != 1 {
		t.Fatalf("page1 total=%d (all=%d) items=%d", total, all, len(items))
	}
	if !items[0].OK {
		t.Fatalf("newest should be success: %+v", items[0])
	}
	_, items2 := page(2)
	if len(items2) != 1 || items2[0].ID == items[0].ID {
		t.Fatalf("page2 should hold next row: %+v vs %+v", items2, items)
	}
}

// TestLoginAlerts 覆盖两类 Herald 告警：新网段成功登录（同日去重）与
// 连续失败达阈值（同 IP 同日至多一条），未配 Herald 也落本地 outbox。
func TestLoginAlerts(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")

	// 基线成功→同网段再成功（不告警）→异网段成功（告警一次）→再次异网段（同日去重）。
	if rec := adminLogin(t, r, "10.1.0.1", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("baseline login: %d", rec.Code)
	}
	if rec := adminLogin(t, r, "10.1.0.2", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("same-net login: %d", rec.Code)
	}
	if rec := adminLogin(t, r, "10.2.0.1", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("new-net login: %d", rec.Code)
	}
	if rec := adminLogin(t, r, "10.3.0.1", "root", "pw-root", ""); rec.Code != http.StatusOK {
		t.Fatalf("second new-net login: %d", rec.Code)
	}
	// 连续失败 5 次（恰达阈值），触发连续失败告警。
	for i := 0; i < 5; i++ {
		if rec := adminLogin(t, r, "10.9.9.9", "root", "wrong", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login %d: %d", i, rec.Code)
		}
	}

	var events []storage.Event
	if err := db.Where("kind = ?", "login_alert").Order("id").Find(&events).Error; err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("login_alert events = %d, want 2 (newip once + brute once): %+v", len(events), events)
	}
	if !strings.Contains(events[0].Title, "新网段") || events[0].Target != "admin" || events[0].Severity != "warning" {
		t.Fatalf("newip event = %+v", events[0])
	}
	if !strings.Contains(events[1].Title, "连续失败") {
		t.Fatalf("brute event = %+v", events[1])
	}
}
