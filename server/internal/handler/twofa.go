package handler

// Admin two-factor authentication (Security Design §1, P1): TOTP follows the 3x-ui approach —
// bound on the dash settings page, re-checked at login (6-digit TOTP or a one-time recovery code);
// unbound admins keep their current login behavior.
// The secret is encrypted via secret_store before being persisted (R24 rule: the master key is
// never stored and never echoed back); recovery codes store only sha256 and are burned on use.
// Binding happens in two steps (setup encrypts and stages the secret in settings -> enable verifies
// one TOTP before writing the user row), avoiding a half-bound state left behind by an unscanned QR code.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	// twofaIssuer is the issuer display name in the otpauth URL.
	twofaIssuer = "ferry"
	// RecoveryCodeCount is the number of one-time recovery codes generated when binding completes.
	RecoveryCodeCount = 8
)

// twofaPendingKey returns the pending-binding key storage key for a given admin (a settings key
// whose value is an encrypted ciphertext). P2 fix: the original single key "admin_2fa_pending" was
// shared across admins, so one admin starting a binding would clobber another's staged secret, and
// the mutual overwrite made the latter's enable verification fail; keys are now partitioned by admin
// ID, so each admin's binding flow is independent.
func twofaPendingKey(userID uint) string {
	return fmt.Sprintf("admin_2fa_pending:%d", userID)
}

// twofaOpts are the TOTP validation parameters (RFC 6238 standard 30s/6 digits/SHA1, with ±1 period skew).
func twofaOpts() totp.ValidateOpts {
	return totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
}

// adminUser fetches the admin account behind the current token; a miss returns 401 (the user was deleted after the token expired).
func (h *Handler) adminUser(c *gin.Context) (storage.User, bool) {
	var u storage.User
	if err := h.db.First(&u, "id = ?", c.GetString("adminUserID")).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "管理员不存在"})
		return u, false
	}
	return u, true
}

// twofaStatus returns the current binding state (for rendering the dash settings page card).
func (h *Handler) twofaStatus(c *gin.Context) {
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": u.TOTPEnabled})
}

// twofaSetup generates a TOTP secret, stages it encrypted in settings, and returns the secret
// string plus the otpauth URL (the dash frontend renders the QR code from the URL). It rejects
// outright when the master key is unconfigured — the secret is never stored in plaintext
// (matching the Security Design §3 encryption-surface rule).
func (h *Handler) twofaSetup(c *gin.Context) {
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	if !h.secrets.Enabled() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "需先配置主密钥 FERRY_SECRET_KEY 才能绑定两步验证（密钥须加密落库）"})
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: twofaIssuer, AccountName: u.Username})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	sealed, err := h.sealSecret(key.Secret())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, twofaPendingKey(u.ID), sealed); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"secret": key.Secret(), "otpauth_url": key.URL()})
}

// twofaEnable confirms binding with one TOTP: only on success is the staged secret written to the
// user row, and one-time recovery codes are generated (the plaintext is returned in this response
// only; only the hash is persisted).
func (h *Handler) twofaEnable(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误：code 必填"})
		return
	}
	// Fetch the current admin first (adminAuthMiddleware already re-checked the DB), then read the
	// staged secret with the per-admin key — after the P2 fix, admins' binding flows no longer clobber each other.
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	sealed, ok, err := storage.GetSetting(h.db, twofaPendingKey(u.ID))
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "尚未发起绑定，请先获取密钥"})
		return
	}
	secretPlain, err := h.secrets.Decrypt(sealed)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if valid, verr := totp.ValidateCustom(in.Code, secretPlain, time.Now(), twofaOpts()); verr != nil || !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证码错误或已过期"})
		return
	}
	// Clear all old codes (rotation on re-binding), then persist the new ones.
	h.db.Where("user_id = ?", u.ID).Delete(&storage.RecoveryCode{})
	u.TOTPSecret = sealed
	u.TOTPEnabled = true
	if err := h.db.Save(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	codes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		code, cerr := newRecoveryCode()
		if cerr != nil {
			fail(c, http.StatusInternalServerError, cerr)
			return
		}
		row := storage.RecoveryCode{UserID: u.ID, CodeHash: hashRecoveryCode(code), CreatedAt: time.Now()}
		if err := h.db.Create(&row).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		codes = append(codes, code)
	}
	h.db.Where("key = ?", twofaPendingKey(u.ID)).Delete(&storage.Setting{})
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

// twofaDisable unbinds: after password confirmation, the secret and recovery codes are cleared
// (so a stolen session cannot casually strip the protection).
func (h *Handler) twofaDisable(c *gin.Context) {
	var in struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(in.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码错误"})
		return
	}
	u.TOTPSecret = ""
	u.TOTPEnabled = false
	if err := h.db.Save(&u).Error; err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	h.db.Where("user_id = ?", u.ID).Delete(&storage.RecoveryCode{})
	h.db.Where("key = ?", twofaPendingKey(u.ID)).Delete(&storage.Setting{})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// checkTwoFA is the login's second-factor gate (called after the password check passes): unbound
// admins pass through; bound admins need a 6-digit TOTP (±1 period skew) or one unused recovery
// code (destroyed on a match, preventing replay).
func (h *Handler) checkTwoFA(u *storage.User, code string) bool {
	if !u.TOTPEnabled {
		return true
	}
	code = strings.TrimSpace(code)
	normalized := normalizeRecoveryCode(code)
	if len(normalized) == 6 && isDigits(normalized) {
		secretPlain, err := h.secrets.Decrypt(u.TOTPSecret)
		if err != nil {
			return false // the master key was rotated and the old secret never sealed: treat as validation failure rather than failing open
		}
		valid, verr := totp.ValidateCustom(code, secretPlain, time.Now(), twofaOpts())
		return verr == nil && valid
	}
	return h.consumeRecoveryCode(u.ID, normalized)
}

// isDigits reports whether the whole string is ASCII digits (an empty string is false).
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// consumeRecoveryCode matches and burns a recovery code: the UPDATE carries a used_at IS NULL
// condition, and RowsAffected=1 means this consumption succeeded (under concurrent replay only one side wins).
func (h *Handler) consumeRecoveryCode(userID uint, normalized string) bool {
	if normalized == "" {
		return false
	}
	res := h.db.Model(&storage.RecoveryCode{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, hashRecoveryCode(normalized)).
		Update("used_at", time.Now())
	return res.Error == nil && res.RowsAffected == 1
}

// newRecoveryCode produces an 8-digit hex recovery code (xxxx-xxxx, from crypto/rand).
func newRecoveryCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	hx := hex.EncodeToString(b)
	return hx[:4] + "-" + hx[4:], nil
}

// normalizeRecoveryCode normalizes typed input: strip spaces/hyphens/underscores and lowercase.
func normalizeRecoveryCode(code string) string {
	r := strings.NewReplacer("-", "", " ", "", "_", "")
	return strings.ToLower(r.Replace(strings.TrimSpace(code)))
}

// hashRecoveryCode stores only sha256 for a recovery code (a one-time random credential; matching goes through this function).
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}
