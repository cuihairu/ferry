package handler

import (
	"crypto/rand"
	"encoding/base64"
)

// RandomToken generates a 256-bit random token (base64url, no padding), used for node
// onboarding and user subscription links; exported so the recovery pipeline L3 can reuse
// the same issuance policy when opening a new machine.
func RandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
