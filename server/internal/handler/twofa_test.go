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

// seedAdmin creates an admin account (same bcrypt convention as AdminLogin).
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

// adminLogin hits the login endpoint with the given IP/UA (UA is fixed for audit assertions).
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

// doJSONAuth sends a request with the admin token (2FA and audit endpoints all live in the authed group).
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

// doJSONAuthID sends a request with a token for the given admin ID (multi-admin cases each use their own).
func doJSONAuthID(t *testing.T, r *gin.Engine, method, path string, body any, adminID uint) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWTFor(t, adminID))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// loginBody parses the login response's token/error code.
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

// totpNow generates a valid TOTP code for the current moment.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	return code
}

// badTotpCode picks a definitely invalid 6-digit code (tested against the real validation
// parameters, avoiding the rare wobble where the ±1 period skew happens to match).
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

// TestTwoFABindLoginRecovery covers the full bind->verify->recovery->unbind flow: setup produces
// the secret and otpauth URL; enable persists the sealed secret and 8 recovery codes after verifying
// a TOTP; once bound, login requires a TOTP or recovery code (missing code totp_required, wrong code
// totp_invalid) and recovery codes burn on use; after unbinding, login passes directly again.
func TestTwoFABindLoginRecovery(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")

	// Unbound state.
	if rec := doJSONAuth(t, r, "GET", "/admin/2fa", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("status unbound: %d %s", rec.Code, rec.Body)
	}

	// setup: secret and otpauth URL.
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

	// A wrong code is rejected; a correct one completes the binding and returns 8 recovery codes.
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
	// The secret is persisted sealed, the staging key cleared, and the recovery code hash rows in place.
	var u storage.User
	db.First(&u, "username = ?", "root")
	if !u.TOTPEnabled || !strings.HasPrefix(u.TOTPSecret, "v1:") {
		t.Fatalf("user twofa: enabled=%v secret=%.20q", u.TOTPEnabled, u.TOTPSecret)
	}
	if _, ok, _ := storage.GetSetting(db, twofaPendingKey(u.ID)); ok {
		t.Fatal("pending secret should be cleared after enable")
	}
	var codeRows int64
	db.Model(&storage.RecoveryCode{}).Where("user_id = ?", u.ID).Count(&codeRows)
	if codeRows != RecoveryCodeCount {
		t.Fatalf("recovery rows = %d, want %d", codeRows, RecoveryCodeCount)
	}

	// Login second factor: missing code, wrong code, correct code, recovery code, recovery code replay.
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

	// Unbind: a wrong password is rejected; the right one clears the secret and recovery codes, and login passes directly again.
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

// TestTwoFASetupNeedsMasterKey covers the encryption-surface rejection: binding reports a clear
// error when the master key is unconfigured (the secret is never stored in plaintext).
func TestTwoFASetupNeedsMasterKey(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")
	if rec := doJSONAuth(t, r, "POST", "/admin/2fa/setup", nil); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "FERRY_SECRET_KEY") {
		t.Fatalf("setup without master key: %d %s", rec.Code, rec.Body)
	}
}

// TestLoginUnboundDirectPass covers the unbound direct pass: an admin without 2FA logs in
// successfully without the totp field, and one login audit row is persisted (IP/UA/result all present).
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

// TestLoginLogsQuery covers audit rows and pagination: failures and successes each get a row, and
// the admin paginated list is ordered new->old with a correct total and page size.
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

// TestLoginAlerts covers the two Herald alert kinds: a successful login from a new network
// segment (deduplicated per day) and consecutive failures reaching the threshold (at most one
// per IP per day); the local outbox is written even when Herald is unconfigured.
func TestLoginAlerts(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdmin(t, db, "root", "pw-root")

	// Baseline success -> another success on the same segment (no alert) -> a different segment succeeds (one alert) -> yet another segment (deduplicated same day).
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
	// 5 consecutive failures (exactly at the threshold), triggering the consecutive-failure alert.
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

// TestTwoFAPendingKeyPerAdmin covers the P2 fix for "one global 2FA key clobbering": the
// pending-binding secret is keyed by admin ID — one admin starting a binding no longer clobbers
// another's staged secret, and each admin's enable validates with their own secret. Before the
// fix, the single "admin_2fa_pending" key was shared across admins: B's setup overwrote A's
// staged secret, and A's enable was rejected on a secret mismatch (binding broken off).
func TestTwoFAPendingKeyPerAdmin(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "admin-a", "pw-a")
	seedAdmin(t, db, "admin-b", "pw-b")
	var a, b storage.User
	if err := db.First(&a, "username = ?", "admin-a").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&b, "username = ?", "admin-b").Error; err != nil {
		t.Fatal(err)
	}

	// A starts a binding -> B starts a binding (must not clobber A's staged secret).
	setupSecret := func(adminID uint) string {
		t.Helper()
		rec := doJSONAuthID(t, r, "POST", "/admin/2fa/setup", nil, adminID)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup admin %d: %d %s", adminID, rec.Code, rec.Body)
		}
		var setup struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &setup); err != nil || setup.Secret == "" {
			t.Fatalf("setup admin %d decode: %v %s", adminID, err, rec.Body)
		}
		return setup.Secret
	}
	secretA := setupSecret(a.ID)
	secretB := setupSecret(b.ID)

	// A enables with A's code: should succeed (before the fix it failed because B's secret overwrote it).
	if rec := doJSONAuthID(t, r, "POST", "/admin/2fa/enable", map[string]any{"code": totpNow(t, secretA)}, a.ID); rec.Code != http.StatusOK {
		t.Fatalf("A enable: %d %s", rec.Code, rec.Body)
	}
	// B enables with B's code: should succeed.
	if rec := doJSONAuthID(t, r, "POST", "/admin/2fa/enable", map[string]any{"code": totpNow(t, secretB)}, b.ID); rec.Code != http.StatusOK {
		t.Fatalf("B enable: %d %s", rec.Code, rec.Body)
	}

	if err := db.First(&a, "username = ?", "admin-a").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&b, "username = ?", "admin-b").Error; err != nil {
		t.Fatal(err)
	}
	if !a.TOTPEnabled || !b.TOTPEnabled {
		t.Fatalf("both admins should be enabled: A=%v B=%v", a.TOTPEnabled, b.TOTPEnabled)
	}
	// Each admin's staging key is cleared, with nothing left over from the other.
	if _, ok, _ := storage.GetSetting(db, twofaPendingKey(a.ID)); ok {
		t.Fatal("A pending should be cleared after enable")
	}
	if _, ok, _ := storage.GetSetting(db, twofaPendingKey(b.ID)); ok {
		t.Fatal("B pending should be cleared after enable")
	}
}
