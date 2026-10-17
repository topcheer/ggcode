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

// #4301: the quoted-body redirect gate fires only when the single-quote body
// DIRECTLY follows an interpreter invocation. Global "sh -c anywhere in the
// command" was wrong (#4273 deploy chain tail runs `-- sh -c $cmd` while its
// earlier PS literals carry `>` as data).
func TestZZIssue4290InterpreterQuoteOpeningAdjacency(t *testing.T) {
	adjacent := []struct {
		cmd string
		qs  int
	}{
		{"sh -c 'echo x > /t'", 6},
		{"bash -c 'x > /t'", 8},
		{"run pwsh -command 'x > /t'", 17},
		{"python -c 'x > /t'", 10},
		{"cmd /c 'x > /t'", 7},
	}
	for _, c := range adjacent {
		if !interpreterQuoteOpening([]rune(c.cmd), c.qs) {
			t.Errorf("interpreterQuoteOpening(%q) = false, want true (direct interpreter opening)", c.cmd)
		}
	}
	// data literals: quote opens right after ordinary words (the #4273
	// -replace pattern shape), even though "sh -c" appears elsewhere in cmd.
	deploy := `$p -replace ' - node: printf > /src/home/.npmrc'; tail -- sh -c $cmd`
	for qs, r := range deploy {
		if r == '\'' {
			if interpreterQuoteOpening([]rune(deploy), qs) {
				t.Errorf("interpreterQuoteOpening(deploy, qs=%d) = true, want false (data literal, not interpreter arg)", qs)
			}
		}
	}
	if interpreterQuoteOpening([]rune("echo 'x > /t'"), 5) {
		t.Errorf("interpreterQuoteOpening(echo literal) = true, want false (no interpreter)")
	}
}
