package secret

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// TestStoreRoundtrip covers R24 semantics: encryption is reversible, the same plaintext yields different ciphertexts (random
// nonce), and the ciphertext carries a version header.
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

// TestStoreKeyIsolation covers rejecting decryption with the wrong key and rejecting a tampered ciphertext.
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

// TestStoreDisabled covers no master key configured: encryption is refused (no plaintext may be persisted), Enabled=false.
func TestStoreDisabled(t *testing.T) {
	s := NewStore("")
	if s.Enabled() {
		t.Fatal("empty master key must be disabled")
	}
	if _, err := s.Encrypt("x"); err == nil {
		t.Fatal("encrypt without master key must fail")
	}
}

// TestStoreBytesRoundtrip covers file-level byte encryption (backup archive semantics, panel availability §2):
// roundtrip restoration, tamper error, empty-key store disabled, and the ciphertext header being the version byte.
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
	// The same plaintext yields different ciphertexts (random nonce)
	ct2, _ := s.EncryptBytes(data)
	if string(ct) == string(ct2) {
		t.Fatal("nonce reuse: identical ciphertext for same plaintext")
	}
	// Tampering must fail
	bad := append([]byte{}, ct...)
	bad[len(bad)-1] ^= 0xff
	if _, err := s.DecryptBytes(bad); err == nil {
		t.Fatal("tampered cipher must fail")
	}
	// An empty-key store is disabled
	if _, err := NewStore("").EncryptBytes(data); err == nil {
		t.Fatal("encrypt bytes without master key must fail")
	}
	// After key rotation the old version is decrypt-only (a v1 header is still decrypted with the v1 key)
	if _, err := NewStore("k1").DecryptBytes(ct); err != nil {
		t.Fatalf("old cipher decrypt with same key: %v", err)
	}
}

// TestDecryptMalformed covers text ciphertext parsing semantics: a missing segment / no version header / bad version / bad nonce /
// bad ciphertext body each errors clearly; an unknown version reports no key; a tampered body reports authentication failure.
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
	// Tampered ciphertext body: the GCM authentication-failure case.
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

// TestDecryptBytesMalformed covers byte ciphertext semantics: an empty archive / unknown version / insufficient length / tampering each
// errors clearly; EncryptBytes is refused outright when no key is configured.
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

// TestEncryptBytesStream covers streaming encryption (P1-8; the backup archive is no longer loaded into memory in whole):
// the output format is byte-for-byte identical to EncryptBytes (same ciphertext length = 1+nonce+len+16,
// solvable by DecryptBytes); crossing the 1MiB streaming buffer boundary and empty input both restore correctly; tampering errors.
// The streaming output passes standard-library GCM authentication, i.e. the GHASH/CTR streaming equivalent implementation is correct.
func TestEncryptBytesStream(t *testing.T) {
	s := NewStore("stream-key")
	cases := [][]byte{
		{},                                     // empty input
		[]byte("small payload"),                // within a single block
		bytes.Repeat([]byte{0xAB}, 1<<20),      // exactly the buffer boundary (1MiB)
		bytes.Repeat([]byte{0x7F}, 1<<20+1234), // crossing the buffer boundary + a non-block-aligned tail
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
	// Equivalent to EncryptBytes: the same input's streaming output is solvable by DecryptBytes (above), and
	// its length structure matches EncryptBytes.
	one := bytes.Repeat([]byte{1}, 4096)
	ctA, _ := s.EncryptBytes(one)
	var outB bytes.Buffer
	if err := s.EncryptBytesStream(bytes.NewReader(one), &outB); err != nil {
		t.Fatal(err)
	}
	if len(ctA) != len(outB.Bytes()) {
		t.Fatalf("encrypt bytes len %d != stream len %d", len(ctA), outB.Len())
	}
	// Tampering the streaming output must fail authentication.
	tam := append([]byte(nil), outB.Bytes()...)
	tam[len(tam)-1] ^= 0xff
	if _, err := s.DecryptBytes(tam); err == nil {
		t.Fatal("tampered stream cipher must fail")
	}
	// An empty-key store is disabled.
	if err := NewStore("").EncryptBytesStream(strings.NewReader("x"), io.Discard); err == nil {
		t.Fatal("stream encrypt without master key must fail")
	}
	// A read-source error propagates upward.
	if err := s.EncryptBytesStream(errReader{}, io.Discard); err == nil {
		t.Fatal("reader error must propagate")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
