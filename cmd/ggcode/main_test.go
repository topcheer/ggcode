package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpShowsAllCommands(t *testing.T) {
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	_ = cmd.Execute()

	help := buf.String()
	// All subcommands should be visible
	expected := []string{
		"version",
		"completion",
		"daemon",
		"harness",
		"im",
		"mcp",
		"llm-probe",
		"acp",
	}
	for _, name := range expected {
		if !strings.Contains(help, name) {
			t.Errorf("root help should list %q, got:\n%s", name, help)
		}
	}
}

func TestVersionSubcommand(t *testing.T) {
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"version"})
	_ = cmd.Execute()
	out := strings.TrimSpace(buf.String())
	if out == "" {
		t.Error("version subcommand should print version")
	}
}
