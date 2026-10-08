package handler

// 管理员两步验证（安全设计 §1，P1）：TOTP 对齐 3x-ui 做法——dash 设置页
// 绑定，登录二次校验（6 位 TOTP 或一次性恢复码），未绑定者登录行为不变。
// 密钥经 secret_store 加密落库（R24 口径：主密钥不入库、不回显），恢复码
// 只存 sha256、用一个销一个；绑定分两步（setup 加密暂存 settings → enable
// 校验一次 TOTP 才落用户行），避免二维码没扫成留下的半绑定态。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	// twofaPendingKey 是绑定中密钥的 settings 暂存键（值为加密密文；单管理员一把）。
	twofaPendingKey = "admin_2fa_pending"
	// twofaIssuer 是 otpauth URL 的 issuer 展示名。
	twofaIssuer = "ferry"
	// RecoveryCodeCount 是绑定完成时生成的一次性恢复码数量。
	RecoveryCodeCount = 8
)

// twofaOpts 是 TOTP 校验参数（RFC 6238 标准 30s/6 位/SHA1，±1 周期容差）。
func twofaOpts() totp.ValidateOpts {
	return totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
}

// adminUser 取当前令牌对应的管理员账号；查不到回 401（令牌过期后用户被删）。
func (h *Handler) adminUser(c *gin.Context) (storage.User, bool) {
	var u storage.User
	if err := h.db.First(&u, "id = ?", c.GetString("adminUserID")).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "管理员不存在"})
		return u, false
	}
	return u, true
}

// twofaStatus 返回当前绑定状态（dash 设置页卡片渲染用）。
func (h *Handler) twofaStatus(c *gin.Context) {
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": u.TOTPEnabled})
}

// twofaSetup 生成 TOTP 密钥，加密暂存 settings 并返回密钥串与 otpauth URL
// （二维码由 dash 前端按 URL 渲染）。主密钥未配置时明确拒绝——密钥永不明文
// 落库（对齐《安全设计》§3 加密面口径）。
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
	if err := storage.SetSetting(h.db, twofaPendingKey, sealed); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"secret": key.Secret(), "otpauth_url": key.URL()})
}

// twofaEnable 用一次 TOTP 确认绑定：校验通过才把暂存密钥落到用户行，并生成
// 一次性恢复码（明文仅本次响应返回，落库只存哈希）。
func (h *Handler) twofaEnable(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Code) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误：code 必填"})
		return
	}
	sealed, ok, err := storage.GetSetting(h.db, twofaPendingKey)
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
	u, ok := h.adminUser(c)
	if !ok {
		return
	}
	// 旧码全清（重绑定轮换），再落新码。
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
	h.db.Where("key = ?", twofaPendingKey).Delete(&storage.Setting{})
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

// twofaDisable 解绑：密码确认后清密钥与恢复码（防会话被盗后顺手拆保护）。
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
	h.db.Where("key = ?", twofaPendingKey).Delete(&storage.Setting{})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// checkTwoFA 是登录的二次因子关（密码校验通过后调用）：未绑定直过；绑定者
// 须 6 位 TOTP（±1 周期容差）或一枚未用恢复码（命中即销毁，防重放）。
func (h *Handler) checkTwoFA(u *storage.User, code string) bool {
	if !u.TOTPEnabled {
		return true
	}
	code = strings.TrimSpace(code)
	normalized := normalizeRecoveryCode(code)
	if len(normalized) == 6 && isDigits(normalized) {
		secretPlain, err := h.secrets.Decrypt(u.TOTPSecret)
		if err != nil {
			return false // 主密钥已换且旧密钥未置：按校验失败处理，不静默放行
		}
		valid, verr := totp.ValidateCustom(code, secretPlain, time.Now(), twofaOpts())
		return verr == nil && valid
	}
	return h.consumeRecoveryCode(u.ID, normalized)
}

// isDigits 报告全为 ASCII 数字（空串为 false）。
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

// consumeRecoveryCode 比对并销毁一枚恢复码：UPDATE 带 used_at IS NULL 条件，
// RowsAffected=1 即本次消费成功（并发重放只有一方成功）。
func (h *Handler) consumeRecoveryCode(userID uint, normalized string) bool {
	if normalized == "" {
		return false
	}
	res := h.db.Model(&storage.RecoveryCode{}).
		Where("user_id = ? AND code_hash = ? AND used_at IS NULL", userID, hashRecoveryCode(normalized)).
		Update("used_at", time.Now())
	return res.Error == nil && res.RowsAffected == 1
}

// newRecoveryCode 产出一枚 8 位十六进制恢复码（xxxx-xxxx，crypto/rand）。
func newRecoveryCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	hx := hex.EncodeToString(b)
	return hx[:4] + "-" + hx[4:], nil
}

// normalizeRecoveryCode 归一化录入形态：去空格/连字符/下划线、转小写。
func normalizeRecoveryCode(code string) string {
	r := strings.NewReplacer("-", "", " ", "", "_", "")
	return strings.ToLower(r.Replace(strings.TrimSpace(code)))
}

// hashRecoveryCode 恢复码只存 sha256（一次性随机凭据，比对走本函数口径）。
func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return hex.EncodeToString(sum[:])
}
