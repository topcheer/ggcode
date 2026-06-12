package main

import (
	"strings"
	"testing"
)

func TestShouldPrintRootHelpDirectly(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: nil, want: false},
		{args: []string{"--help"}, want: true},
		{args: []string{"-h"}, want: true},
		{args: []string{"help"}, want: true},
		{args: []string{"completion", "--help"}, want: false},
		{args: []string{"--config", "x"}, want: false},
	}

	for _, tt := range tests {
		if got := shouldPrintRootHelpDirectly(tt.args); got != tt.want {
			t.Fatalf("shouldPrintRootHelpDirectly(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestRootHelpTextIncludesExpectedSections(t *testing.T) {
	help := rootHelpText()
	want := []string{
		"Usage:\nggcode [flags]\nggcode [command]\n",
		"Available Commands:\n- completion: Generate shell completion script\n- daemon: Run ggcode in daemon mode, controlled via IM\n- harness: Manage harness-engineering workflows\n- help: Help about any command\n- im: Manage IM adapters, bindings, and pairing\n- mcp: Manage MCP server configuration\n",
		"Flags:\n",
		"- --config string: config file path\n",
	}
	for _, snippet := range want {
		if !strings.Contains(help, snippet) {
			t.Fatalf("expected help text to contain %q, got:\n%s", snippet, help)
		}
	}
}
