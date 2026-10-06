package main

// #3457 probe: install runs with DisableFlagParsing, so the inherited
// --config flag lands in the raw args and sanitizeMCPInstallArgs must HAND
// IT BACK (not drop it) so RunE honors the user's target config path. The
// old version silently discarded it and Save() wrote the default path while
// exit 0 claimed success - fail-silent misdirect.

import (
	"reflect"
	"testing"
)

func TestIssue3457_SanitizeHandsBackConfigValue(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		args []string
		cfg  string
	}{
		{
			name: "tail space form",
			in:   []string{"srv", "stdio", "--config", "/alt.yaml", "--", "npx", "-y", "x"},
			args: []string{"srv", "stdio", "--", "npx", "-y", "x"},
			cfg:  "/alt.yaml",
		},
		{
			name: "equals form",
			in:   []string{"--config=/b.yaml", "srv", "http", "https://x.example"},
			args: []string{"srv", "http", "https://x.example"},
			cfg:  "/b.yaml",
		},
		{
			name: "leading form (root-level position)",
			in:   []string{"--config", "/c.yaml", "srv", "stdio", "--", "run"},
			args: []string{"srv", "stdio", "--", "run"},
			cfg:  "/c.yaml",
		},
		{
			name: "no flag untouched",
			in:   []string{"srv", "stdio", "--", "npx", "-y", "x"},
			args: []string{"srv", "stdio", "--", "npx", "-y", "x"},
			cfg:  "",
		},
		{
			name: "bare --config without value",
			in:   []string{"srv", "--config"},
			args: []string{"srv"},
			cfg:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, cfg := sanitizeMCPInstallArgs(tc.in)
			if cfg != tc.cfg {
				t.Fatalf("config value = %q, want %q", cfg, tc.cfg)
			}
			if !reflect.DeepEqual(args, tc.args) {
				t.Fatalf("args = %v, want %v", args, tc.args)
			}
		})
	}
}
