package certwatch

import (
	"encoding/pem"
	"testing"
)

// TestParsePEMRejectsNonPEM 非 PEM 内容：不解析不出状态。
func TestParsePEMRejectsNonPEM(t *testing.T) {
	if _, ok := parsePEM([]byte("hello, not a certificate")); ok {
		t.Fatal("non-PEM input must be rejected")
	}
}

// TestParsePEMRejectsBadDER PEM 块内不是合法证书 DER：同样拒绝。
func TestParsePEMRejectsBadDER(t *testing.T) {
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{0x01, 0x02, 0x03}})
	if _, ok := parsePEM(raw); ok {
		t.Fatal("garbage DER inside PEM must be rejected")
	}
}
