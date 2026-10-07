package secret

import (
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
