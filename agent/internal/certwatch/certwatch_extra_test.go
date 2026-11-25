package certwatch

import (
	"encoding/pem"
	"testing"
)

// TestParsePEMRejectsNonPEM covers non-PEM content: nothing is parsed and no state comes out.
func TestParsePEMRejectsNonPEM(t *testing.T) {
	if _, ok := parsePEM([]byte("hello, not a certificate")); ok {
		t.Fatal("non-PEM input must be rejected")
	}
}

// TestParsePEMRejectsBadDER covers a PEM block whose DER is not a valid certificate: rejected the same way.
func TestParsePEMRejectsBadDER(t *testing.T) {
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{0x01, 0x02, 0x03}})
	if _, ok := parsePEM(raw); ok {
		t.Fatal("garbage DER inside PEM must be rejected")
	}
}
