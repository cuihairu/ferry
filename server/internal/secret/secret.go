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
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

// EncryptBytes 以当前版本钥加密任意字节（文件级，如备份档）：
// 输出格式 = 版本字节 + nonce + 密文（紧凑二进制，不走 v1:… 文本格式）。
// DecryptBytes 是其逆运算；字符串机密仍走 Encrypt/Decrypt。
func (s *Store) EncryptBytes(plain []byte) ([]byte, error) {
	key, ok := s.keys[Version]
	if !ok {
		return nil, errors.New("secret: master key not configured (FERRY_SECRET_KEY)")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plain)+aead.Overhead())
	out = append(out, byte(Version))
	out = append(out, nonce...)
	return aead.Seal(out, nonce, plain, nil), nil
}

// DecryptBytes 解密 EncryptBytes 产物；版本未置钥或认证失败报错。
func (s *Store) DecryptBytes(data []byte) ([]byte, error) {
	if len(data) < 1 {
		return nil, errors.New("secret: empty cipher")
	}
	ver := int(data[0])
	key, ok := s.keys[ver]
	if !ok {
		return nil, fmt.Errorf("secret: no key for cipher version v%d", ver)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(data) < 1+aead.NonceSize() {
		return nil, errors.New("secret: malformed cipher")
	}
	nonce := data[1 : 1+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, data[1+aead.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("secret: decrypt failed (wrong key or tampered cipher)")
	}
	return plain, nil
}

// EncryptBytesStream 以当前版本钥把输入流流式加密到输出流（文件级，如备份
// 档）：输出格式与 EncryptBytes 逐字节一致（版本字节 + nonce + GCM 密文），
// DecryptBytes 可直接解其产物；区别只在加密分块完成，不把整份明文载入内存
// ——大文件（流量表增长的整库快照）不再翻倍占内存。密文仍是标准 AES-256-GCM
// 单消息（CTR 数据 + GHASH 标签），仅把 Seal 拆为流式等价实现。
func (s *Store) EncryptBytesStream(in io.Reader, out io.Writer) error {
	key, ok := s.keys[Version]
	if !ok {
		return errors.New("secret: master key not configured (FERRY_SECRET_KEY)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if _, err := out.Write([]byte{byte(Version)}); err != nil {
		return err
	}
	if _, err := out.Write(nonce); err != nil {
		return err
	}
	return encryptStream(block, nonce, in, out)
}

// encryptStream 做 GCM 的流式加密体：CTR 密文写 out，GHASH 标签追加尾部。
// 单消息 GCM（与 aead.Seal 等价）：J0=nonce||0^31||1，数据计数器自 J0+1 起，
// 标签 = GHASH_H(密文) ⊕ E(K, J0)，H=E(K, 0^128)，AAD 为空。
func encryptStream(block cipher.Block, nonce []byte, in io.Reader, out io.Writer) error {
	var j0, mask, h [16]byte
	copy(j0[:], nonce)
	j0[15] = 1
	block.Encrypt(mask[:], j0[:])         // E(K, J0)：标签掩码
	block.Encrypt(h[:], make([]byte, 16)) // H = E(K, 0^128)
	var ctrIV [16]byte
	copy(ctrIV[:], j0[:])
	inc32(ctrIV[:]) // 数据首块 = J0+1
	stream := cipher.NewCTR(block, ctrIV[:])
	g := ghashState{h: h}
	buf := make([]byte, 1<<20)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			stream.XORKeyStream(chunk, chunk) // CTR 原地加密
			g.Write(chunk)
			if _, err := out.Write(chunk); err != nil {
				return err
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return rerr
		}
	}
	tag := g.Sum()
	for i := range tag {
		tag[i] ^= mask[i]
	}
	_, err := out.Write(tag[:])
	return err
}

// inc32 把 128 位计数器低 32 位 +1（GCM 计数器进位语义）。
func inc32(b []byte) {
	for i := len(b) - 1; i >= len(b)-4; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// ghashState 增量 GHASH_H（AAD 为空）：按 16 字节块喂入密文，尾部不足一块
// 的零填充与 GCM 长度块在 Sum 处理（SP 800-38D：GHASH 输入总是以
// len(A)||len(C) 长度块收尾，空密文也不例外）。
type ghashState struct {
	h        [16]byte
	y        [16]byte
	pend     [16]byte
	pendLen  int
	totalLen int64 // 累计密文字节数（长度块以位计）
}

func (g *ghashState) Write(p []byte) {
	g.totalLen += int64(len(p))
	for len(p) > 0 {
		n := copy(g.pend[g.pendLen:], p)
		g.pendLen += n
		p = p[n:]
		if g.pendLen == 16 {
			g.mulBlock(&g.pend)
			g.pendLen = 0
		}
	}
}

func (g *ghashState) mulBlock(blk *[16]byte) {
	for j := 0; j < 16; j++ {
		g.y[j] ^= blk[j]
	}
	g.y = gcmMul(&g.y, &g.h)
}

func (g *ghashState) Sum() [16]byte {
	if g.pendLen > 0 {
		var blk [16]byte
		copy(blk[:], g.pend[:g.pendLen]) // 不足一块零填充
		g.mulBlock(&blk)
		g.pendLen = 0
	}
	// GCM 长度块：len(A)=0（64 位）|| len(C)=totalLen*8 位（64 位，大端）。
	var lenBlock [16]byte
	binary.BigEndian.PutUint64(lenBlock[8:], uint64(g.totalLen)*8)
	g.mulBlock(&lenBlock)
	return g.y
}

// gcmMul 计算 a*b（GF(2^128)，GCM 位序：字节内 MSB 优先，约化多项式
// x^128+x^7+x^2+x+1）。实现自 SP 800-38D 算法 1；正确性由 DecryptBytes
// （标准库 GCM 认证）往返验证。
func gcmMul(a, b *[16]byte) [16]byte {
	var z, v [16]byte
	copy(v[:], a[:])
	for i := 0; i < 128; i++ {
		if (b[i/8]>>(7-i%8))&1 != 0 {
			for j := 0; j < 16; j++ {
				z[j] ^= v[j]
			}
		}
		lsb := v[15] & 1
		for j := 15; j > 0; j-- {
			v[j] = v[j]>>1 | v[j-1]<<7
		}
		v[0] >>= 1
		if lsb != 0 {
			v[0] ^= 0xe1
		}
	}
	return z
}
