package agent

import (
	"strings"
	"testing"
)

// CVE-2026-103572 class (VS/SSMS Copilot `.mcp.json.` config-protection
// bypass, Adversa 2026-11): Windows path normalization strips trailing
// dots/spaces from every component, so `ggcode.yaml.` resolves back to
// `ggcode.yaml` while an exact-match self-mod guard sees a different name.
// The guard must normalize trailing dots/spaces per component before
// matching, and the workspace `.mcp.json` must itself be a protected
// pattern (sa-85 gaps 1+2).
func TestSelfMod_TrailingDotBypassBlocked(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		contains string
	}{
		{"trailing dot on config", "ggcode.yaml.", "config"},
		{"trailing dots on config", "ggcode.yaml..", "config"},
		{"trailing space on config", "ggcode.yaml ", "config"},
		{"trailing dot+space on config", "ggcode.yaml. ", "config"},
		{"trailing dot on memory file", "AGENTS.md.", "memory"},
		{"trailing dot on slash pattern", ".ggcode/config.", "config"},
		{"mcp.json plain", ".mcp.json", "MCP server config"},
		{"mcp.json under root prefix", "/repo/.mcp.json", "MCP server config"},
		{"mcp.json trailing dot", ".mcp.json.", "MCP server config"},
	}
	for _, tc := range cases {
		s := newSelfModState()
		s.setRoot("/repo")
		msg := s.checkSelfModification("write_file", rawMsg(t, map[string]interface{}{"path": tc.path}))
		if msg == "" {
			t.Errorf("%s: no warning for %q (bypass not blocked)", tc.name, tc.path)
			continue
		}
		if !strings.Contains(msg, tc.contains) {
			t.Errorf("%s: warning for %q lost category %q: %s", tc.name, tc.path, tc.contains, msg)
		}
	}
}

// Benign paths that merely contain dots must stay unflagged.
func TestSelfMod_InteriorDotsUnflagged(t *testing.T) {
	for _, path := range []string{
		"src/foo.bar/file.go",
		"notes/config.discussion.md",
		"main_test.go",
	} {
		s := newSelfModState()
		if msg := s.checkSelfModification("write_file", rawMsg(t, map[string]interface{}{"path": path})); msg != "" {
			t.Errorf("unexpected warning for benign path %q: %s", path, msg)
		}
	}
}
