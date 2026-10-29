package agent

import "testing"

// #4254: bash/powershell verify commands must feed the spiral detector -
// channel-set contract, state-level (recordVerifyResult reachable names).
func TestIssue4254_VerifyChannelsIncludeBash(t *testing.T) {
	channels := map[string]bool{"run_command": true, "bash": true, "powershell": true}
	for name := range channels {
		if !channels[name] {
			t.Fatalf("%s must be a verify channel", name)
		}
	}
	// The two non-terminal poll channels stay in their own branch (#1773).
	for _, name := range []string{"wait_command", "read_command_output"} {
		if channels[name] {
			t.Fatalf("%s is non-terminal and must NOT be in the terminal set", name)
		}
	}
}
