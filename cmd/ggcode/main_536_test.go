package main

import "testing"

func TestShouldRedirectStderr(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"TUI launch (no args)", nil, true},
		{"TUI launch with flags", []string{"--bypass"}, true},
		{"short pipe flag", []string{"-p", "do work"}, false},
		{"long pipe flag", []string{"--prompt", "do work"}, false},
		{"long pipe flag with value", []string{"--prompt=do work"}, false},
		{"pipe flag after other flags", []string{"--bypass", "-p", "hi"}, false},
		{"version subcommand", []string{"version"}, false},
		{"daemon subcommand", []string{"daemon", "start"}, false},
		{"unknown-but-subcommand-shaped arg", []string{"mcp"}, false},
		{"flag before TUI", []string{"-c", "/tmp/x.yaml"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRedirectStderr(tt.args); got != tt.want {
				t.Errorf("shouldRedirectStderr(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
