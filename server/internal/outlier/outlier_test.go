package outlier

import (
	"reflect"
	"testing"
)

// TestTrim covers F-0916's rules: small sets pass through untouched, a single
// spike is trimmed, a degenerate zero-MAD set keeps only the median value,
// and the input slice is never mutated.
func TestTrim(t *testing.T) {
	cases := []struct {
		name    string
		samples []int
		want    []int
	}{
		{"too few to judge", []int{10, 900, 12}, []int{10, 900, 12}},
		{"single spike dropped", []int{20, 21, 20, 22, 20, 900}, []int{20, 21, 20, 22, 20}},
		{"all equal", []int{20, 20, 20, 20}, []int{20, 20, 20, 20}},
		{"all equal plus spike", []int{20, 20, 20, 20, 900}, []int{20, 20, 20, 20}},
		{"normal spread untouched", []int{18, 20, 22, 19, 21, 20}, []int{18, 20, 22, 19, 21, 20}},
	}
	for _, tc := range cases {
		got := Trim(tc.samples, DefaultFactor)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: Trim(%v) = %v, want %v", tc.name, tc.samples, got, tc.want)
		}
	}
	// The input must survive: callers re-read it for sample counts.
	input := []int{20, 21, 20, 22, 20, 900}
	_ = Trim(input, DefaultFactor)
	if len(input) != 6 || input[5] != 900 {
		t.Fatalf("Trim must not mutate input: %v", input)
	}
}
