package permission

import "testing"

// #4290: Windows shells surface with display-name casing ("Bash"/"PowerShell")
// because display name and tool name are same-source on the Windows side; a
// case-sensitive switch silently skipped every ConfigPolicy command gate
// (IsExtremelyDangerous, credentialPayloadExfil, sensitive-redirect) for
// those tool names. The comparison must be case-insensitive across the full
// command-tool family.
func TestZZIssue4290IsCommandToolCaseInsensitive(t *testing.T) {
	must := []string{
		"run_command", "start_command", "tmux", "ghostty", "warp", "kitty", "iterm2",
		"bash", "powershell", // family extension (#4290)
		"Bash", "PowerShell", // Windows display-name casing (the reported repro)
		"BASH", "Run_Command", // arbitrary casing must not create gate bypasses
	}
	for _, n := range must {
		if !isCommandTool(n) {
			t.Errorf("isCommandTool(%q) = false, want true (command gates must cover it)", n)
		}
	}
	mustNot := []string{
		"wait_command", "read_command_output", // read-only: never execute new command text
		"write_command_input",                        // excluded by design (#197)
		"bashful", "powershellx", "run_commands", "", // prefix/suffix lookalikes must not match
	}
	for _, n := range mustNot {
		if isCommandTool(n) {
			t.Errorf("isCommandTool(%q) = true, want false (not a command-execution tool)", n)
		}
	}
}
