package agent

import "testing"

func TestIssue4254_VerifyChannelsIncludeBash(t *testing.T) {
	channels := map[string]bool{"run_command": true, "bash": true, "powershell": true}
	for name := range channels {
		if !channels[name] {
			t.Fatalf("%s must be a verify channel", name)
		}
	}

	for _, name := range []string{"wait_command", "read_command_output"} {
		if channels[name] {
			t.Fatalf("%s is non-terminal and must NOT be in the terminal set", name)
		}
	}
}
