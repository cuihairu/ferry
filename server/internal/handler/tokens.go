package handler

import (
	"crypto/rand"
	"encoding/base64"
)

// randomToken 生成 256 位随机令牌（base64url 无填充），用于节点接入与用户订阅链接。
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
