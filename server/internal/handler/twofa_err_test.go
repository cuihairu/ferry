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

// Thirty-fourth batch gap fill: two-factor (P1) error paths — status/disable with a missing admin
// 401, setup staging persist failure, enable bad binding and empty code / staged-secret read fault /
// binding never initiated / staged secret undecryptable / admin missing after validation passed /
// user row save failure / recovery code persist failure, disable bad binding / missing admin /
// wrong password / save failure, checkTwoFA failing closed on an undecryptable old ciphertext,
// and isDigits / consumeRecoveryCode empty-value short circuits.
// Left out on purpose: crypto/rand errors from totp.Generate and newRecoveryCode, and a
// sealSecret failure after Enabled has already been judged (AES-GCM with a master key always succeeds) — not forced in.

// base32TestSecret is a valid base32 test secret (only used to generate codes that skip validation).
const base32TestSecret = "JBSWY3DPEHPK3PXP"

// isRecoveryCodeModel reports whether a statement lands on the recovery code table.
func isRecoveryCodeModel(v any) bool {
	switch v.(type) {
	case *storage.RecoveryCode, []storage.RecoveryCode, *[]storage.RecoveryCode:
		return true
	}
	return false
}

// seedPendingTOTP writes a verifiable pending-binding secret directly (so enable gets through its
// validation segment); it is staged under the per-admin ID key (after the P2 key split, enable reads by its own ID).
func seedPendingTOTP(t *testing.T, db *gorm.DB, master string, adminID uint) string {
	t.Helper()
	gen, err := totp.Generate(totp.GenerateOpts{Issuer: twofaIssuer, AccountName: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secret.NewStore(master).Encrypt(gen.Secret())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Setting{Key: twofaPendingKey(adminID), Value: sealed}).Error; err != nil {
		t.Fatal(err)
	}
	return gen.Secret()
}

// TestTwoFAMissingAdmin the token points at a nonexistent admin: status and disable both 401.
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

// TestTwoFASetupErrors setup staging persist failure gives 500.
func TestTwoFASetupErrors(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")
	failSettingAt(t, db, "test:pending_set", 1)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/setup", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableValidation enable's validation surface: bad binding/empty code, staged-secret
// read fault, binding never initiated, and an undecryptable staged secret.
func TestTwoFAEnableValidation(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	seedAdmin(t, db, "root", "pw-root")

	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable", "not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", rec.Code, rec.Body)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable", map[string]any{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code: %d %s", rec.Code, rec.Body)
	}
	// Binding never initiated (no staged secret) -> 400.
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusBadRequest {
		t.Fatalf("no pending: %d %s", rec.Code, rec.Body)
	}
	// Staged-secret read fault -> 500.
	failQueryOn(t, db, "test:pending_q", isSettingModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("get setting: %d %s", rec.Code, rec.Body)
	}

	// Staged secret undecryptable (not a versioned ciphertext) -> 500.
	r, db = newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	root := seedAdmin(t, db, "root", "pw-root")
	if err := db.Create(&storage.Setting{Key: twofaPendingKey(root.ID), Value: "garbage"}).Error; err != nil {
		t.Fatal(err)
	}
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, base32TestSecret)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("decrypt: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableDBFails enable's persistence segment: user row save failure and recovery code persist failure.
func TestTwoFAEnableDBFails(t *testing.T) {
	master := "test-master-key"
	// User row save failure (validation already passed).
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = master })
	root := seedAdmin(t, db, "root", "pw-root")
	plain := seedPendingTOTP(t, db, master, root.ID)
	failUpdateOn(t, db, "test:twofa_user_update", isUserModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save user: %d %s", rec.Code, rec.Body)
	}

	// Recovery code persist failure (user row already saved).
	r, db = newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = master })
	root = seedAdmin(t, db, "root", "pw-root")
	plain = seedPendingTOTP(t, db, master, root.ID)
	failCreateOn(t, db, "test:recovery_create", isRecoveryCodeModel)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create codes: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFAEnableMissingAdmin the token's account no longer exists after enable validation passed -> 401.
func TestTwoFAEnableMissingAdmin(t *testing.T) {
	r, db := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })
	plain := seedPendingTOTP(t, db, "test-master-key", 1)
	if rec := doJSONAuth(t, r, http.MethodPost, "/admin/2fa/enable",
		map[string]any{"code": totpNow(t, plain)}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing admin: %d %s", rec.Code, rec.Body)
	}
}

// TestTwoFADisableErrors unbinding: bad binding, wrong password, and save failure.
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

// TestTwoFAHelpers helper short circuits: an undecryptable TOTP ciphertext fails closed (no fail
// open), isDigits tri-state, and an empty recovery code is never consumed.
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
	// A one-time recovery code is burned on its first hit: the same code no longer matches the second time.
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
