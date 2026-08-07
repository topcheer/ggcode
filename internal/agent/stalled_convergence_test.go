package agent

import (
	"testing"
)

func TestStalledConvergence_Detected(t *testing.T) {
	s := newStalledConvergenceState()

	// Simulate: 20 -> 15 (big improvement), then 15 -> 14, then 14 -> 14
	// This shows a decelerating pattern that should trigger.
	s.recordEdit()
	got := s.recordVerify(buildResultWithErrors(15), true)
	if got != "" {
		t.Fatal("should not fire on first verification (no history)")
	}

	s.recordEdit()
	got = s.recordVerify(buildResultWithErrors(14), true)
	if got != "" {
		t.Fatal("should not fire on second verification (insufficient samples)")
	}

	s.recordEdit()
	got = s.recordVerify(buildResultWithErrors(14), true)
	// 3rd sample: deltas are -5, -1, 0 -- both recent deltas < 30% of peak
	if got == "" {
		t.Fatal("should detect stalled convergence with decelerating pattern")
	}
}

func TestStalledConvergence_NotStalled_StillProgressing(t *testing.T) {
	s := newStalledConvergenceState()

	// 20 -> 10 -> 8 -> 6: still good progress, deltas are consistent
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)

	s.recordEdit()
	s.recordVerify(buildResultWithErrors(8), true)

	s.recordEdit()
	got := s.recordVerify(buildResultWithErrors(6), true)
	if got != "" {
		t.Fatal("should not fire when progress is still strong: " + got)
	}
}

func TestStalledConvergence_NotStalled_Increasing(t *testing.T) {
	s := newStalledConvergenceState()

	// 10 -> 12 -> 14: errors increasing -- not convergence
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(12), true)

	s.recordEdit()
	s.recordVerify(buildResultWithErrors(14), true)

	s.recordEdit()
	got := s.recordVerify(buildResultWithErrors(16), true)
	if got != "" {
		t.Fatal("should not fire when errors are increasing")
	}
}

func TestStalledConvergence_MaxWarnings(t *testing.T) {
	s := newStalledConvergenceState()

	// Fire first warning
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(15), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(14), true)
	s.recordEdit()
	got1 := s.recordVerify(buildResultWithErrors(14), true)
	if got1 == "" {
		t.Fatal("first warning should fire")
	}

	// Continue stalled pattern
	s.recordEdit()
	got2 := s.recordVerify(buildResultWithErrors(14), true)
	if got2 == "" {
		t.Fatal("second warning should fire")
	}

	// Third should be capped
	s.recordEdit()
	got3 := s.recordVerify(buildResultWithErrors(14), true)
	if got3 != "" {
		t.Fatal("third warning should be capped (maxStalledWarnings=2)")
	}
}

func TestStalledConvergence_Reset(t *testing.T) {
	s := newStalledConvergenceState()
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(10), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(9), true)
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(9), true)

	s.reset()
	if len(s.errorHistory) != 0 || s.warningCount != 0 || s.hadEdits {
		t.Fatal("reset should clear all state")
	}
}

func TestStalledConvergence_NoEdits(t *testing.T) {
	s := newStalledConvergenceState()

	// Verifications without edits should not trigger
	s.recordVerify(buildResultWithErrors(15), true)
	s.recordVerify(buildResultWithErrors(14), true)
	got := s.recordVerify(buildResultWithErrors(14), true)
	if got != "" {
		t.Fatal("should not fire without edits between verifications")
	}
}

func TestStalledConvergence_Pass(t *testing.T) {
	s := newStalledConvergenceState()

	// Verification that passes should not trigger
	s.recordEdit()
	s.recordVerify(buildResultWithErrors(5), true)

	s.recordEdit()
	s.recordVerify(buildResultWithErrors(3), true)

	s.recordEdit()
	got := s.recordVerify("OK\n", false) // passes
	if got != "" {
		t.Fatal("should not fire when verification passes")
	}
}

func TestIsStalledConvergence(t *testing.T) {
	tests := []struct {
		name     string
		history  []int
		expected bool
	}{
		{"clear stall", []int{20, 15, 14, 14}, true},
		{"stall to zero delta", []int{10, 5, 4, 4}, true},
		{"still progressing", []int{20, 10, 5, 2}, false},
		{"increasing", []int{5, 10, 15, 20}, false},
		{"too few samples", []int{10, 5}, false},
		{"no meaningful improvement", []int{0, 0, 0, 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isStalledConvergence(tt.history)
			if got != tt.expected {
				t.Errorf("isStalledConvergence(%v) = %v, want %v", tt.history, got, tt.expected)
			}
		})
	}
}

// buildResultWithErrors creates a fake verification output with N error lines.
func buildResultWithErrors(n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += "Error: something failed on line " + stalledItoa(i) + "\n"
	}
	return result
}

// stalledItoa is a minimal int-to-string for test helpers.
func stalledItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
