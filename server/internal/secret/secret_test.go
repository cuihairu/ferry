package secret

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// TestStoreRoundtrip 覆盖 R24 口径：加密可逆、同明文两次密文不同（随机
// nonce）、密文带版本头。
func TestStoreRoundtrip(t *testing.T) {
	s := NewStore("master-key-001")
	ct1, err := s.Encrypt("sk-live-abcdef")
	if err != nil {
		t.Fatal(err)
	}
	ct2, err := s.Encrypt("sk-live-abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct1, "v1:") {
		t.Fatalf("cipher prefix = %s", ct1)
	}
	if ct1 == ct2 {
		t.Fatal("same plain must yield different ciphers (random nonce)")
	}
	plain, err := s.Decrypt(ct1)
	if err != nil || plain != "sk-live-abcdef" {
		t.Fatalf("decrypt = %q err=%v", plain, err)
	}
}

// TestStoreKeyIsolation 覆盖错钥拒解与篡改拒解。
func TestStoreKeyIsolation(t *testing.T) {
	s1 := NewStore("key-one")
	s2 := NewStore("key-two")
	ct, err := s1.Encrypt("payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Decrypt(ct); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := s1.Decrypt(ct[:len(ct)-4] + "AAAA"); err == nil {
		t.Fatal("tampered cipher must fail")
	}
	if _, err := s1.Decrypt("not-a-cipher"); err == nil {
		t.Fatal("malformed cipher must fail")
	}
}

// TestStoreDisabled 覆盖未配主密钥：拒绝加密（禁止明文落库），Enabled=false。
func TestStoreDisabled(t *testing.T) {
	s := NewStore("")
	if s.Enabled() {
		t.Fatal("empty master key must be disabled")
	}
	if _, err := s.Encrypt("x"); err == nil {
		t.Fatal("encrypt without master key must fail")
	}
}

// TestStoreBytesRoundtrip 覆盖文件级字节加密（备份档口径，面板可用性 §2）：
// roundtrip 还原、篡改报错、空钥禁用、密文头为版本字节。
func TestStoreBytesRoundtrip(t *testing.T) {
	s := NewStore("k1")
	data := []byte("\x00\x01ferry-backup-bytes\xff")
	ct, err := s.EncryptBytes(data)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ct) < 2 || ct[0] != Version {
		t.Fatalf("cipher head = %v, want version byte %d", ct[:1], Version)
	}
	got, err := s.DecryptBytes(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(data) {
		t.Fatal("roundtrip mismatch")
	}
	// 同明文两次密文不同（随机 nonce）
	ct2, _ := s.EncryptBytes(data)
	if string(ct) == string(ct2) {
		t.Fatal("nonce reuse: identical ciphertext for same plaintext")
	}
	// 篡改必须失败
	bad := append([]byte{}, ct...)
	bad[len(bad)-1] ^= 0xff
	if _, err := s.DecryptBytes(bad); err == nil {
		t.Fatal("tampered cipher must fail")
	}
	// 空钥仓禁用
	if _, err := NewStore("").EncryptBytes(data); err == nil {
		t.Fatal("encrypt bytes without master key must fail")
	}
	// 换钥后旧版本仅解密可用（v1 头仍由 v1 钥解）
	if _, err := NewStore("k1").DecryptBytes(ct); err != nil {
		t.Fatalf("old cipher decrypt with same key: %v", err)
	}
}

// TestDecryptMalformed 文本密文解析口径：缺段/无版本头/坏版本号/坏 nonce/
// 坏密文体各自明确报错；未知版本报无钥；篡改密文体报认证失败。
func TestDecryptMalformed(t *testing.T) {
	s := NewStore("master-key-001")
	for _, bad := range []string{
		"", "garbage", "v1:onlytwo",
		"x1:aa:bb", "vX:aa:bb",
		"v1:!!:bb", "v1:aa:!!",
	} {
		if _, err := s.Decrypt(bad); err == nil {
			t.Errorf("Decrypt(%q) = nil error", bad)
		}
	}
	if _, err := s.Decrypt("v9:aa:bb"); err == nil ||
		!strings.Contains(err.Error(), "no key for cipher version") {
		t.Errorf("unknown version err = %v; want no-key error", err)
	}
	// 篡改密文体：GCM 认证失败口径。
	good, err := s.Encrypt("payload")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(good, ":")
	raw, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xff
	tampered := parts[0] + ":" + parts[1] + ":" + base64.RawStdEncoding.EncodeToString(raw)
	if _, err := s.Decrypt(tampered); err == nil ||
		!strings.Contains(err.Error(), "decrypt failed") {
		t.Errorf("tampered err = %v; want decrypt failed", err)
	}
}

// TestDecryptBytesMalformed 字节密文口径：空档/未知版本/长度不足/篡改各自
// 明确报错；未配钥时 EncryptBytes 直接拒。
func TestDecryptBytesMalformed(t *testing.T) {
	s := NewStore("master-key-001")
	if _, err := s.DecryptBytes(nil); err == nil ||
		!strings.Contains(err.Error(), "empty cipher") {
		t.Errorf("empty err = %v; want empty cipher", err)
	}
	if _, err := s.DecryptBytes([]byte{9, 1, 2, 3}); err == nil ||
		!strings.Contains(err.Error(), "no key for cipher version") {
		t.Errorf("unknown version err = %v; want no-key error", err)
	}
	enc, err := s.EncryptBytes([]byte("file-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecryptBytes(enc[:3]); err == nil ||
		!strings.Contains(err.Error(), "malformed cipher") {
		t.Errorf("truncated err = %v; want malformed", err)
	}
	tam := append([]byte(nil), enc...)
	tam[len(tam)-1] ^= 0xff
	if _, err := s.DecryptBytes(tam); err == nil ||
		!strings.Contains(err.Error(), "decrypt failed") {
		t.Errorf("tampered err = %v; want decrypt failed", err)
	}
	if _, err := NewStore("").EncryptBytes([]byte("x")); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Errorf("disabled store err = %v; want not configured", err)
	}
}

// TestEncryptBytesStream 覆盖流式加密（P1-8，备份档不再整份载入内存）：
// 输出格式与 EncryptBytes 逐字节一致（同密文长度 = 1+nonce+len+16，
// DecryptBytes 可解），跨越 1MiB 流式缓冲边界与空输入均正确还原；篡改报错。
// 流式产物能被标准库 GCM 认证通过，即 GHASH/CTR 流式等价实现正确。
func TestEncryptBytesStream(t *testing.T) {
	s := NewStore("stream-key")
	cases := [][]byte{
		{},                                     // 空输入
		[]byte("small payload"),                // 单块内
		bytes.Repeat([]byte{0xAB}, 1<<20),      // 恰好缓冲边界（1MiB）
		bytes.Repeat([]byte{0x7F}, 1<<20+1234), // 跨缓冲边界 + 非整块尾
	}
	for i, data := range cases {
		var out bytes.Buffer
		if err := s.EncryptBytesStream(bytes.NewReader(data), &out); err != nil {
			t.Fatalf("case %d encrypt stream: %v", i, err)
		}
		ct := out.Bytes()
		if len(ct) != 1+12+len(data)+16 {
			t.Fatalf("case %d cipher len = %d, want %d", i, len(ct), 1+12+len(data)+16)
		}
		if ct[0] != Version {
			t.Fatalf("case %d version byte = %d", i, ct[0])
		}
		got, err := s.DecryptBytes(ct)
		if err != nil {
			t.Fatalf("case %d decrypt stream output: %v", i, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("case %d roundtrip mismatch (got %d bytes, want %d)", i, len(got), len(data))
		}
	}
	// 与 EncryptBytes 等价：同输入流式产物 DecryptBytes 可解（上文），且
	// 长度结构与 EncryptBytes 一致。
	one := bytes.Repeat([]byte{1}, 4096)
	ctA, _ := s.EncryptBytes(one)
	var outB bytes.Buffer
	if err := s.EncryptBytesStream(bytes.NewReader(one), &outB); err != nil {
		t.Fatal(err)
	}
	if len(ctA) != len(outB.Bytes()) {
		t.Fatalf("encrypt bytes len %d != stream len %d", len(ctA), outB.Len())
	}
	// 篡改流式产物必须认证失败。
	tam := append([]byte(nil), outB.Bytes()...)
	tam[len(tam)-1] ^= 0xff
	if _, err := s.DecryptBytes(tam); err == nil {
		t.Fatal("tampered stream cipher must fail")
	}
	// 空钥仓禁用。
	if err := NewStore("").EncryptBytesStream(strings.NewReader("x"), io.Discard); err == nil {
		t.Fatal("stream encrypt without master key must fail")
	}
	// 读源报错向上传播。
	if err := s.EncryptBytesStream(errReader{}, io.Discard); err == nil {
		t.Fatal("reader error must propagate")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
