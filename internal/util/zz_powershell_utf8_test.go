package util

import (
	"context"
	"strings"
	"testing"
)

func TestPowerShellPrefixPinsUTF8(t *testing.T) {
	cmd, spec, err := NewShellCommandContext(context.Background(), "Write-Output hi")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "powershell" && spec.Name != "sh" {
		t.Fatalf("unexpected shell %q", spec.Name)
	}

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
