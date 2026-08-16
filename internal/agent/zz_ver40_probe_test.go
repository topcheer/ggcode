package agent

import "testing"

// zz_ver40_ probe: verify suspected business-logic bugs. DELETE AFTER RUN.

// (C) premature_success: plain exact-token match is NOT restricted to command
// position (#483 only fixed hyphen/underscore variants).
func TestZzVer40_PrematureSuccessArgPositionTokenArms(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
		desc string
	}{
		{"grep -n test main.go", false, "grep for word 'test' at arg position"},
		{"git add test", false, "adding a file literally named 'test'"},
		{"ls build", false, "listing a dir named 'build'"},
		{"go test ./...", true, "real verification"},
		{`echo "verify"`, false, "quoted word"},
		{"cat verify-config.yaml", false, "#483 hyphen case"},
	}
	for _, c := range cases {
		got := psIsVerifyCommand(c.cmd)
		t.Logf("cmd=%q got=%v want=%v (%s)", c.cmd, got, c.want, c.desc)
		if got != c.want {
			t.Errorf("MISMATCH: %q -> %v, want %v (%s)", c.cmd, got, c.want, c.desc)
		}
	}
}

// End-to-end: arg-position token arms everVerified and silences the detector.
func TestZzVer40_PrematureSuccessSilenced(t *testing.T) {
	p := newPrematureSuccessState()
	p.recordToolCall("edit_file", map[string]interface{}{}, false)
	// Agent greps for the word "test" (not a verification) and it succeeds.
	p.recordToolCall("run_command", map[string]interface{}{"command": "grep -n test main.go"}, false)
	// Agent then claims success.
	got := p.checkSuccessClaim("All tests pass. The issue is fixed.")
	t.Logf("guidance after grep 'test': %q", got)
	if got == "" {
		t.Errorf("BUG: success claim NOT flagged after fake 'verification' (grep for word 'test')")
	}
}

// (A) token_waste: negative-marker substring without word boundary.
func TestZzVer40_NegativeMarkerSubstring(t *testing.T) {
	cases := []string{
		"unclean working tree",
		"failed to cleanup artifacts",
		"cleanup errors remain",
	}
	for _, c := range cases {
		t.Logf("isNegativeResult(%q) = %v", c, isNegativeResult(c))
	}
}

// (A) hint inflation: guidance text appended to result.Content before
// recordToolResult inflates the recorded token count of the same result.
func TestZzVer40_HintInflation(t *testing.T) {
	s := newTokenWasteBudgetState()
	base := "short"
	withHint := base + "\n\n[redundant-read] long guidance text here padding padding padding padding"
	s.recordToolResult("read_file", base, false, false, nil)
	s.recordToolResult("read_file", withHint, false, false, nil)
	t.Logf("base tokens=%d, hinted tokens=%d for same underlying result", estimateTokens(base), estimateTokens(withHint))
}

// (B) conflict detector: mutual exclusion regression + word-start anchoring.
func TestZzVer40_ConflictCases(t *testing.T) {
	cases := []struct {
		hints []string
		want  bool
		desc  string
	}{
		{[]string{"[A] STOP EXPLORING, act now", "[B] stop exploring further and edit"}, false, "same-direction stop-exploring (#462)"},
		{[]string{"[A] STOP EXPLORING, act now", "[B] READ MORE before editing"}, true, "genuine explore/act conflict"},
		{[]string{"[A] VERIFY your changes", "[B] reduce iterations, fewer calls"}, true, "verify vs speed conflict"},
	}
	for _, c := range cases {
		got := detectGuidanceConflict(c.hints) != ""
		t.Logf("%s: conflict=%v want=%v", c.desc, got, c.want)
		if got != c.want {
			t.Errorf("MISMATCH: %s -> %v want %v", c.desc, got, c.want)
		}
	}
}
