package handler

import (
	"crypto/rand"
	"encoding/base64"
)

// RandomToken 生成 256 位随机令牌（base64url 无填充），用于节点接入与用户订阅链接；
// 导出供恢复流水线 L3 开新机复用同一签发口径。
func RandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
