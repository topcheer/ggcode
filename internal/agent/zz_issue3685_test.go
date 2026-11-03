package agent

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
