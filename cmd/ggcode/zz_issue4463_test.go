package main

import "testing"

// zz_issue4463_test.go - companion test for #4463.
// record_effectiveness accepted any float64: only a type assertion plus
// int(f) truncation, no range check anywhere in the chain
// (RecordSkillEffectiveness forwards, usage_tracker appends, avgScore
// averages raw values) - a WebUI call with {"score": -100} or {"score":
// 1e9} silently poisoned the persisted last-10 statistics.
func TestIssue4463ParseKnightScore(t *testing.T) {
	// Valid scores, including both boundaries and rounding.
	for in, want := range map[float64]int{
		1: 1, 5: 5, 3: 3, 4.9: 5, 1.4: 1, 2.5: 3,
	} {
		got, err := parseKnightScore(map[string]interface{}{"score": in})
		if err != nil {
			t.Fatalf("parseKnightScore(%v) unexpected error: %v", in, err)
		}
		if got != want {
			t.Errorf("parseKnightScore(%v) = %d, want %d", in, got, want)
		}
	}
	// Out-of-range scores must be rejected, not recorded.
	for _, in := range []float64{0, -1, -100, 6, 1e9} {
		if _, err := parseKnightScore(map[string]interface{}{"score": in}); err == nil {
			t.Errorf("parseKnightScore(%v) accepted out-of-range score, want error", in)
		}
	}
	// Missing score falls back to neutral 3 (pre-existing behavior).
	if got, err := parseKnightScore(map[string]interface{}{}); err != nil || got != 3 {
		t.Errorf("parseKnightScore(missing) = %d, %v; want 3, nil", got, err)
	}
	// Non-float score type falls back to neutral 3 (pre-existing behavior).
	if got, err := parseKnightScore(map[string]interface{}{"score": "4"}); err != nil || got != 3 {
		t.Errorf("parseKnightScore(string) = %d, %v; want 3, nil", got, err)
	}
}
