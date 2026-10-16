package agent

import "testing"

// #4253: non-terminal poll channels must not count as verification events
// for strategyFixation - enum removal contract.
func TestIssue4253_PollChannelsNotVerification(t *testing.T) {
	for _, name := range []string{"wait_command", "read_command_output"} {
		if strategyFixationIsVerification(name) {
			t.Fatalf("%s must NOT be a bare verification event (non-terminal poll; #2992 mirror owns terminal outcomes)", name)
		}
	}
	for _, name := range []string{"run_command", "bash", "powershell", "code_health"} {
		if !strategyFixationIsVerification(name) {
			t.Fatalf("%s must remain a verification channel", name)
		}
	}
}
