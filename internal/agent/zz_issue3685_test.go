package agent

// #3685 probes (unit-level pins of the enum unification):
//  A) isCommandChannelTool recognizes the six command channels - the
//     verifyDebt/editPropagation clear gates in agent.go key off it, so a
//     `go test` through bash/powershell now clears debt like run_command.
//  B) strategyFixationIsVerification mirrors the causal-gate channels MINUS
//     start_command: its launch result only means "job started" and used to
//     clear streaks before any test ran; the real event is the terminal
//     outcome (wait_command/read_command_output), now wired via the #2992
//     branch.

import "testing"

func TestIssue3685_CommandChannelSet(t *testing.T) {
	for _, name := range []string{"run_command", "bash", "powershell", "start_command", "wait_command", "read_command_output"} {
		if !isCommandChannelTool(name) {
			t.Fatalf("%s must be a command channel", name)
		}
	}
	for _, name := range []string{"read_file", "grep", "edit_file", "web_fetch"} {
		if isCommandChannelTool(name) {
			t.Fatalf("%s must NOT be a command channel", name)
		}
	}
}

func TestIssue3685_StrategyFixationEnumUnified(t *testing.T) {
	if strategyFixationIsVerification("start_command") {
		t.Fatal("start_command must NOT count as verification at launch (#3685-B reverse distortion)")
	}
	for _, name := range []string{"run_command", "bash", "powershell", "wait_command", "read_command_output", "code_health", "review_changes", "verify", "lsp_diagnostics"} {
		if !strategyFixationIsVerification(name) {
			t.Fatalf("%s must be a verification channel", name)
		}
	}
}
