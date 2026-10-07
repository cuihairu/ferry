package cardcode

import (
	"regexp"
	"testing"
)

var codeRe = regexp.MustCompile(`^([2-9A-HJKMNP-Z]{4})-([2-9A-HJKMNP-Z]{4})-([2-9A-HJKMNP-Z]{4})$`)

func TestGenerate(t *testing.T) {
	codes, err := Generate(1000)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(codes) != 1000 {
		t.Fatalf("count = %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if !codeRe.MatchString(c) {
			t.Fatalf("bad format: %q", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code: %q", c)
		}
		seen[c] = true
	}
	if n, err := Generate(0); err != nil || len(n) != 0 {
		t.Fatalf("zero batch = %v, %v", n, err)
	}
}
