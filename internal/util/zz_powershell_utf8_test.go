package util

import (
	"context"
	"strings"
	"testing"
)

// Guard for the Windows PowerShell UTF-8 pinning: every powershell command
// must be prefixed with console/pipe/file encoding fixes so agent-driven
// file edits through PowerShell cannot silently rewrite UTF-8 files into
// GBK/UTF-16 (mass file corruption on non-English Windows).
func TestPowerShellPrefixPinsUTF8(t *testing.T) {
	cmd, spec, err := NewShellCommandContext(context.Background(), "Write-Output hi")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "powershell" && spec.Name != "sh" {
		t.Fatalf("unexpected shell %q", spec.Name)
	}
	// On this test host the shell may be sh (non-Windows). Only the
	// powershell path carries the prefix; verify via the args we control.
	full := strings.Join(cmd.Args, " ")
	if spec.Name == "powershell" {
		for _, want := range []string{
			"[Console]::OutputEncoding=[Text.Encoding]::UTF8",
			"[Console]::InputEncoding=[Text.Encoding]::UTF8",
			"$OutputEncoding=[Text.Encoding]::UTF8",
			"$PSDefaultParameterValues['*:Encoding']='utf8'",
			"$ErrorActionPreference='Continue'",
		} {
			if !strings.Contains(full, want) {
				t.Errorf("powershell prefix missing %q in %q", want, full)
			}
		}
	}
}
