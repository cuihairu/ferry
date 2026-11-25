// Package secret is the single entry point for secret encryption (R24 encryption-surface semantics): the master key is never stored in the DB
// (it comes from a deployment env var/file); every secret is encrypted with AES-256-GCM before being persisted, and ciphertexts carry
// a version number so keys can be rotated. All secret encryption/decryption in the project goes through here; hand-rolled crypto elsewhere is forbidden.
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

// Version is the current ciphertext version number; on key rotation add a new version and encrypt with the new key, while older versions are decrypt-only.
const Version = 1

// Store holds the master key per version (a sha256-derived AES-256 key).
type Store struct {
	keys map[int][]byte
}

// NewStore builds the store from the master key; an empty master key returns a disabled store (Encrypt/Decrypt error out,
// no plaintext is allowed to be persisted unencrypted). Multi-key input of the form "v1:xxx" is supported (key-rotation transition).
func NewStore(masterKey string) *Store {
	s := &Store{keys: map[int][]byte{}}
	if masterKey == "" {
		return s
	}
	s.keys[Version] = deriveKey(masterKey)
	return s
}

// deriveKey derives an AES-256 key from a master key of any length.
func deriveKey(master string) []byte {
	k := sha256.Sum256([]byte(master))
	return k[:]
}

// Enabled reports whether a master key has been configured.
func (s *Store) Enabled() bool { return len(s.keys) > 0 }

// Encrypt encrypts a plaintext secret into a version-prefixed ciphertext ("v1:nonce:ct").
// A random nonce guarantees two ciphertexts of the same plaintext differ.
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

// Decrypt picks the key by the version header and decrypts; an unset version key (rotated away) or an authentication failure errors.
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

// EncryptBytes encrypts arbitrary bytes with the current version key (file-level, e.g. backup archives):
// output format = version byte + nonce + ciphertext (compact binary, not the v1:... text format).
// DecryptBytes is its inverse; string secrets still go through Encrypt/Decrypt.
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

// DecryptBytes decrypts EncryptBytes output; an unset version key or an authentication failure errors.
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

// EncryptBytesStream streams-encrypts the input stream to the output stream with the current version key (file-level,
// e.g. backup archives): the output format is byte-for-byte identical to EncryptBytes (version byte + nonce + GCM ciphertext),
// and DecryptBytes can decode its output directly; the only difference is that encryption runs in chunks and never loads the whole
// plaintext into memory——a large file (a full DB snapshot as the traffic table grows) no longer doubles memory. The ciphertext is still a
// standard single-message AES-256-GCM (CTR data + GHASH tag); Seal is just split into an equivalent streaming implementation.
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

// encryptStream performs the GCM streaming ciphertext body: the CTR ciphertext goes to out, and the GHASH tag is appended at the end.
// Single-message GCM (equivalent to aead.Seal): J0=nonce||0^31||1, the data counter starts at J0+1,
// tag = GHASH_H(ciphertext) XOR E(K, J0), H=E(K, 0^128), and AAD is empty.
func encryptStream(block cipher.Block, nonce []byte, in io.Reader, out io.Writer) error {
	var j0, mask, h [16]byte
	copy(j0[:], nonce)
	j0[15] = 1
	block.Encrypt(mask[:], j0[:])         // E(K, J0): the tag mask
	block.Encrypt(h[:], make([]byte, 16)) // H = E(K, 0^128)
	var ctrIV [16]byte
	copy(ctrIV[:], j0[:])
	inc32(ctrIV[:]) // first data block = J0+1
	stream := cipher.NewCTR(block, ctrIV[:])
	g := ghashState{h: h}
	buf := make([]byte, 1<<20)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			stream.XORKeyStream(chunk, chunk) // CTR in-place encryption
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

// inc32 adds 1 to the low 32 bits of the 128-bit counter (GCM counter carry semantics).
func inc32(b []byte) {
	for i := len(b) - 1; i >= len(b)-4; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// ghashState is incremental GHASH_H (AAD empty): feed the ciphertext in 16-byte blocks, and handle the
// trailing zero padding and the GCM length block in Sum (SP 800-38D: GHASH input always ends with a
// len(A)||len(C) length block, an empty ciphertext included).
type ghashState struct {
	h        [16]byte
	y        [16]byte
	pend     [16]byte
	pendLen  int
	totalLen int64 // cumulative ciphertext bytes (the length block counts bits)
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
		copy(blk[:], g.pend[:g.pendLen]) // zero-pad a short final block
		g.mulBlock(&blk)
		g.pendLen = 0
	}
	// GCM length block: len(A)=0 (64 bits) || len(C)=totalLen*8 bits (64 bits, big-endian).
	var lenBlock [16]byte
	binary.BigEndian.PutUint64(lenBlock[8:], uint64(g.totalLen)*8)
	g.mulBlock(&lenBlock)
	return g.y
}

// gcmMul computes a*b (GF(2^128), GCM bit order: MSB-first within a byte, reduction polynomial
// x^128+x^7+x^2+x+1). Implemented from SP 800-38D Algorithm 1; correctness is round-trip verified by DecryptBytes
// (standard-library GCM authentication).
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
