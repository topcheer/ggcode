package config

import "testing"

// #5050: the hint regex consumed each match's trailing boundary char, so a
// later, larger k/m token in the same model name lost its leading boundary
// and never matched - the take-max silently degraded to the smaller hint
// (8k-128k -> 8000, a 16x underestimate).
func TestIssue5050LaterLargerTokenWins(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"testmodel-8k-128k", 128000},
		{"glm-4-9k-200k", 200000},
		// Later SMALLER token still loses to the earlier larger one.
		{"big-1m-small-8k", 1000000},
		// Single tokens unchanged.
		{"plain-32k", 32000},
		{"huge-2m", 2000000},
		// No hint -> 0 (legacy fallback path).
		{"totally-unrelated-model", 0},
		// Adjacent tokens separated only by the boundary char still both match.
		{"8k,128k", 128000},
	}
	for _, tc := range cases {
		if got := parseContextWindowHint(tc.model); got != tc.want {
			t.Fatalf("parseContextWindowHint(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}
