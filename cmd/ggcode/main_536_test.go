package main

import "testing"

// #536 Bug D: pipe mode and non-TUI subcommands must NOT redirect os.Stderr
// into the debug ring buffer. Previously main() redirected stderr
// unconditionally, so every RunPipe error path (provider resolution failure,
// stdin read failure, agent errors) wrote into a capture pipe whose only
// consumer was debug.Log — without GGCODE_DEBUG those writes never reached
// the real stderr, leaving CI/scripts with exit code 1 and zero diagnostics.
// The restore-before-exit logic was structurally unreachable because RunPipe
// calls os.Exit from inside cobra's RunE.
//
// The gate below is what keeps os.Stderr pristine for those modes, which is
// exactly what makes pipe-mode errors visible on the caller's stderr.
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
