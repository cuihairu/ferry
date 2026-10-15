package handler

import (
	"net/http"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
)

// 三十四批补口：两步验证（P1）错误口径——状态/解绑查无管理员 401、setup
// 落库失败、enable 坏绑定与空码/取暂存库故障/尚未发起绑定/暂存不可解/
// 校验通过后查无管理员/用户行保存失败/恢复码落库失败、disable 坏绑定/
// 查无/密码错误/保存失败、checkTwoFA 旧密文解不开按失败、isDigits 与
// consumeRecoveryCode 空值短路。
// 留白：totp.Generate 与 newRecoveryCode 的 crypto/rand 错误、sealSecret
// 已判 Enabled 后再失败（AES-GCM 有主密钥恒成功）不硬凑。

// base32TestSecret 是合法 base32 测试密钥（仅用于生成不参与校验的码）。
const base32TestSecret = "JBSWY3DPEHPK3PXP"

// isRecoveryCodeModel 判断语句是否落在恢复码表上。
func isRecoveryCodeModel(v any) bool {
	switch v.(type) {
	case *storage.RecoveryCode, []storage.RecoveryCode, *[]storage.RecoveryCode:
		return true
	}
	return false
}

// seedPendingTOTP 直落一枚可校验的绑定中密钥（供 enable 走通校验段）。
func seedPendingTOTP(t *testing.T, db *gorm.DB, master string) string {
	t.Helper()
	gen, err := totp.Generate(totp.GenerateOpts{Issuer: twofaIssuer, AccountName: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secret.NewStore(master).Encrypt(gen.Secret())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Setting{Key: twofaPendingKey, Value: sealed}).Error; err != nil {
		t.Fatal(err)
	}
	return gen.Secret()
}

// TestTwoFAMissingAdmin 令牌指向不存在的管理员：状态与解绑 401。
func TestTwoFAMissingAdmin(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if rec := doJSONAuth(t, r, http.MethodGet, "/admin/2fa", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/disable",
		map[string]any{"password": "pw-root"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFASetupErrors setup 暂存落库失败 500。
func TestTwoFASetupErrors(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")
	failSettingAt(t, db, "test:pending_set", 1)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/setup", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableValidation enable 的校验面：坏绑定/空码、取暂存库故障、
// 尚未发起绑定与暂存不可解。
func TestTwoFAEnableValidation(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")

	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d %s", rec.Code, rec.Body)
	}
	// 尚未发起绑定（无暂存密钥）→ 400。
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no pending: %d %s", rec.Code, rec.Body)
	}
	// 取暂存库故障 → 500。
	failQueryOn(t, db, "test:pending_q", isSettingModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get setting: %d %s", rec.Code, rec.Body)
	}

	// 暂存不可解（不是带版本的密文）→ 500。
	r, db = newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")
	if err := db.Create(&storage.Setting{Key: twofaPendingKey, Value: "garbage"}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("decrypt: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableDBFails enable 的落库段：用户行保存失败与恢复码落库失败。
func TestTwoFAEnableDBFails(t *testing.T) {
	master := "test-master-key"
	// 用户行保存失败（校验已过）。
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = master })
	seedAdmin(t, db, "root", "pw-root")
	plain := seedPendingTOTP(t, db, master)
	failUpdateOn(t, db, "test:twofa_user_update", isUserModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save user: %d %s", rec.Code, rec.Body)
	}

	// 恢复码落库失败（用户行已保存）。
	r, db = newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = master })
	seedAdmin(t, db, "root", "pw-root")
	plain = seedPendingTOTP(t, db, master)
	failCreateOn(t, db, "test:recovery_create", isRecoveryCodeModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create codes: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableMissingAdmin enable 校验通过后令牌账号已不存在 → 401。
func TestTwoFAEnableMissingAdmin(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	plain := seedPendingTOTP(t, db, "test-master-key")
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing admin: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFADisableErrors 解绑：坏绑定、密码错误与保存失败。
func TestTwoFADisableErrors(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/disable", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/disable",
		map[string]any{"password": "wrong"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: %d %s", rec.Code, rec.Body)
	}
	failUpdateOn(t, db, "test:twofa_disable_update", isUserModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/disable",
		map[string]any{"password": "pw-root"}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAHelpers 助手短路：TOTP 密文解不开按校验失败（不静默放行）、
// isDigits 三态与空恢复码不消费。
func TestTwoFAHelpers(t *testing.T) {
	_, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	h := &Handler{db: db, secrets: secret.NewStore("")}
	u := storage.User{TOTPEnabled: true, TOTPSecret: "v1:abcd:efgh"}
	if h.checkTwoFA(&u, "123456") {
		t.Fatal("undecryptable secret must fail closed")
	}
	if !h.checkTwoFA(&storage.User{}, "123456") {
		t.Fatal("unbound admin must pass")
	}
	if isDigits("") || isDigits("12a456") || !isDigits("123456") {
		t.Fatal("isDigits tri-state mismatch")
	}
	if h.consumeRecoveryCode(1, "") {
		t.Fatal("empty code must not consume")
	}
	// 一次性恢复码命中即销：同码第二次不再命中。
	u = storage.User{}
	if err := db.Create(&storage.RecoveryCode{UserID: 7, CodeHash: hashRecoveryCode("abcd-efgh")}).Error; err != nil {
		t.Fatal(err)
	}
	if !h.consumeRecoveryCode(7, "abcd-efgh") {
		t.Fatal("first consume must succeed")
	}
	if h.consumeRecoveryCode(7, "abcd-efgh") {
		t.Fatal("second consume must fail")
	}
}
