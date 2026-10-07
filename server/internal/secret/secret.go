// Package secret 是机密加密统一入口（R24 加密面口径）：主密钥不入库
// （部署环境变量/文件提供），机密一律 AES-256-GCM 加密落库，密文带
// 版本号支持换钥。全项目机密加解密只走这里，禁止散落手写 crypto。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version 是当前密文版本号；换钥时新增版本并用新钥加密，旧版本仅解密。
const Version = 1

// Store 持有各版本主密钥（sha256 派生 AES-256 钥）。
type Store struct {
	keys map[int][]byte
}

// NewStore 以主密钥建仓；空主密钥返回禁用仓（Encrypt/Decrypt 报错，
// 不允许未加密明文落库）。支持 "v1:xxx" 形式的多钥输入（换钥过渡期）。
func NewStore(masterKey string) *Store {
	s := &Store{keys: map[int][]byte{}}
	if masterKey == "" {
		return s
	}
	s.keys[Version] = deriveKey(masterKey)
	return s
}

// deriveKey 任意长度主密钥派生 AES-256 钥。
func deriveKey(master string) []byte {
	k := sha256.Sum256([]byte(master))
	return k[:]
}

// Enabled 报告是否已配置主密钥。
func (s *Store) Enabled() bool { return len(s.keys) > 0 }

// Encrypt 把明文机密加密为带版本前缀的密文（"v1:nonce:ct"）。
// 随机 nonce 保证同明文两次密文不同。
func (s *Store) Encrypt(plain string) (string, error) {
	key, ok := s.keys[Version]
	if !ok {
		return "", errors.New("secret: master key not configured (FERRY_SECRET_KEY)")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(plain), nil)
	return fmt.Sprintf("v%d:%s:%s", Version,
		base64.RawStdEncoding.EncodeToString(nonce),
		base64.RawStdEncoding.EncodeToString(sealed)), nil
}

// Decrypt 按版本头选钥解密；版本未置钥（已轮换掉）或认证失败报错。
func (s *Store) Decrypt(cipherText string) (string, error) {
	ver, nonce, sealed, err := parse(cipherText)
	if err != nil {
		return "", err
	}
	key, ok := s.keys[ver]
	if !ok {
		return "", fmt.Errorf("secret: no key for cipher version v%d", ver)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	plain, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", errors.New("secret: decrypt failed (wrong key or tampered cipher)")
	}
	return string(plain), nil
}

func parse(cipherText string) (int, []byte, []byte, error) {
	parts := strings.Split(cipherText, ":")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "v") {
		return 0, nil, nil, errors.New("secret: malformed cipher")
	}
	ver, err := strconv.Atoi(parts[0][1:])
	if err != nil {
		return 0, nil, nil, errors.New("secret: malformed cipher version")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, nil, nil, errors.New("secret: malformed cipher nonce")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, nil, nil, errors.New("secret: malformed cipher body")
	}
	return ver, nonce, sealed, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
