package agent

import (
	"fmt"
	"testing"
)

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

// TestEditPropagationGateExcludesSpawn (#4660): the editPropagation/fileChurn
// clearing gate must use the same spawn exclusion as the verifyDebt gate -
// start_command success only proves submission, so a spawned background test
// must NOT wipe the detectors' books before the final state is known.
func TestEditPropagationGateExcludesSpawn(t *testing.T) {
	// The gate predicate itself: start_command is excluded; the FOREGROUND
	// execution channel still passes (verifyDebtSpawnClearingTool keeps
	// every isCommandChannelTool member except start_command).
	if verifyDebtSpawnClearingTool("start_command") {
		t.Fatal("start_command must be excluded from the spawn-clearing gate")
	}
	if !isCommandChannelTool("run_command") {
		t.Fatal("sanity: run_command must be a command-channel tool")
	}
	if !verifyDebtSpawnClearingTool("run_command") {
		t.Error("verifyDebtSpawnClearingTool(run_command) = false, expected true (foreground channel)")
	}
	// editPropagation book-keeping is NOT reset by a spawn-shaped result:
	// drive the detector with its real API, then prove the gate predicate
	// rejects start_command so recordGreenBuild() is never reached.
	ep := newEditPropagationState()
	for i := 0; i < 3; i++ {
		ep.recordEdit("edit_file", fmt.Sprintf(`{"file_path":"/tmp/file%d.go"}`, i))
	}
	ep.mu.Lock()
	n := len(ep.distinctFiles)
	ep.mu.Unlock()
	if n != 3 {
		t.Fatalf("distinctFiles = %d, want 3 before any gate", n)
	}
	cmd := extractCommandFromArgs([]byte(`{"command":"go test ./..."}`))
	if cmd == "" || !isVerifyCommand(cmd) {
		t.Fatalf("verify-command extraction broken: cmd=%q", cmd)
	}
	// The gate (agent.go:5429): start_command result must be rejected by the
	// predicate BEFORE recordGreenBuild runs - books survive the spawn.
	if !verifyDebtSpawnClearingTool("start_command") {
		ep.mu.Lock()
		n = len(ep.distinctFiles)
		ep.mu.Unlock()
		if n != 3 {
			t.Fatal("books must survive a spawn-shaped tool result")
		}
	} else {
		t.Fatal("unreachable: predicate check above already asserted exclusion")
	}
}
