package agent

import "testing"

// Regression guard for #4649: start_command's successful result must NOT
// clear verification debt at the tool-result wiring site. Pre-fix,
// agent.go gated only on isCommandChannelTool(tc.Name) - which includes
// start_command - so spawning a background `go test` zeroed editsSinceGreen
// and bumped totalGreenBuilds before the test outcome existed. The same
// run's later failed background test then produced no verifyDebt warning,
// and if the agent did wait, the terminal-poll branch (#2992) cleared the
// debt a second time (double-counted green). This contradicted the explicit
// start_command exclusions at the #3682 scopeNarrow and #3989
// recordSuccessfulCommand call sites in the same file.
func TestVerifyDebt_SpawnSuccessMustNotClearDebt(t *testing.T) {
	cases := []struct {
		tool string
		want bool
	}{
		// The core #4649 exclusion: spawn success is not a verification event.
		{"start_command", false},
		// Foreground command channels still clear on success (#3685-A).
		{"run_command", true},
		{"bash", true},
		{"powershell", true},
		// Poll channels are not spawn events; they are consumed by the
		// bgVerifyJobs terminal branch, not this predicate (and clearing on
		// a bare poll success would have the same premature semantics).
		{"wait_command", true},
		{"read_command_output", true},
		// Non-command tools never clear here.
		{"read_file", false},
		{"edit_file", false},
	}
	for _, tc := range cases {
		if got := verifyDebtSpawnClearingTool(tc.tool); got != tc.want {
			t.Errorf("verifyDebtSpawnClearingTool(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}
	// Predicate must stay consistent with the channel set: every command
	// channel except start_command is spawn-clearing.
	for _, ch := range []string{"run_command", "bash", "powershell", "wait_command", "read_command_output"} {
		if !isCommandChannelTool(ch) {
			t.Errorf("isCommandChannelTool(%q) = false, expected true (set drift)", ch)
		}
	}
}

// The end-to-end semantics being protected: a passing foreground verify
// command clears editsSinceGreen (this behavior is unchanged); nothing in
// the fix may have weakened the foreground path.
func TestVerifyDebt_ForegroundVerifyStillClears(t *testing.T) {
	s := newVerifyDebtState()
	s.recordSourceEdit()
	s.recordSourceEdit()
	if s.editsSinceGreen != 2 {
		t.Fatalf("editsSinceGreen = %d, want 2", s.editsSinceGreen)
	}
	s.recordVerifyCommand("go test ./...", false)
	if s.editsSinceGreen != 0 {
		t.Errorf("foreground passing go test must clear editsSinceGreen, got %d", s.editsSinceGreen)
	}
}
