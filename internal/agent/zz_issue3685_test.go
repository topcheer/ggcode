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
