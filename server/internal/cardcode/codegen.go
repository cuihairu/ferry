// Package cardcode generates card codes ("payment design" §3.2): crypto/rand plus an ambiguity-removed character set.
package cardcode

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// charset removes easily-confused characters such as 0/1/I/L/O, 32 in total, exactly 5 bits.
const charset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// codeLen is the character count of one card code, grouped as XXXX-XXXX-XXXX.
const codeLen = 12

// Generate produces n plaintext card codes. When randomness is insufficient the whole batch errors out rather than issuing a weak code.
func Generate(n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	raw := make([]byte, codeLen*n)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		for j := 0; j < codeLen; j++ {
			if j > 0 && j%4 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(charset[int(raw[i*codeLen+j])%len(charset)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}
