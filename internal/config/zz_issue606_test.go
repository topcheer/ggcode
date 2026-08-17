package config

import "testing"

// TestIssue606PatchExplicitClearVsAbsent: patchMCPServerConfig must
// distinguish "field absent" from "field explicitly cleared" for
// Args/Env/Headers. nil means absent (keep the old value, #249 semantics);
// an empty non-nil slice/map means the user cleared it (#606 A3 — before
// this fix, deleting every env line in the desktop form and saving silently
// kept the old env, because len()>0 guards treated empty as absent).
func TestIssue606PatchExplicitClearVsAbsent(t *testing.T) {
	base := MCPServerConfig{
		Name:    "s",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "some-mcp"},
		Env:     map[string]string{"API_KEY": "x"},
		Headers: map[string]string{"Authorization": "Bearer t"},
	}

	// Absent (nil) fields are preserved.
	keep := &Config{MCPServers: []MCPServerConfig{base}}
	keep.UpsertMCPServer(MCPServerConfig{Name: "s", Type: "stdio", Command: "node"})
	got := keep.MCPServers[0]
	if len(got.Args) != 2 {
		t.Fatalf("absent Args must be preserved, got %v", got.Args)
	}
	if got.Env["API_KEY"] != "x" || len(got.Env) != 1 {
		t.Fatalf("absent Env must be preserved, got %v", got.Env)
	}
	if got.Headers["Authorization"] != "Bearer t" || len(got.Headers) != 1 {
		t.Fatalf("absent Headers must be preserved, got %v", got.Headers)
	}

	// Empty non-nil fields are an explicit clear.
	clear := &Config{MCPServers: []MCPServerConfig{base}}
	clear.UpsertMCPServer(MCPServerConfig{
		Name:    "s",
		Type:    "stdio",
		Command: "node",
		Args:    []string{},
		Env:     map[string]string{},
		Headers: map[string]string{},
	})
	got = clear.MCPServers[0]
	if len(got.Args) != 0 {
		t.Fatalf("explicit clear of Args not applied, got %v", got.Args)
	}
	if len(got.Env) != 0 {
		t.Fatalf("explicit clear of Env not applied, got %v", got.Env)
	}
	if len(got.Headers) != 0 {
		t.Fatalf("explicit clear of Headers not applied, got %v", got.Headers)
	}
	// Unrelated fields still patched normally.
	if got.Command != "node" {
		t.Fatalf("Command patch lost, got %q", got.Command)
	}
}

// TestIssue606PatchPreservesStringFields: string fields keep the #249
// "empty means not provided" semantics — the A3 fix only changes collection
// fields, so a form that omits the command must not wipe it.
func TestIssue606PatchPreservesStringFields(t *testing.T) {
	base := MCPServerConfig{
		Name:    "s",
		Type:    "http",
		Command: "old",
		URL:     "https://example.test/mcp",
	}
	cfg := &Config{MCPServers: []MCPServerConfig{base}}
	cfg.UpsertMCPServer(MCPServerConfig{
		Name: "s",
		Type: "http",
		URL:  "https://new.example.test/mcp",
	})
	got := cfg.MCPServers[0]
	if got.Command != "old" {
		t.Fatalf("empty Command in patch must keep existing value, got %q", got.Command)
	}
	if got.URL != "https://new.example.test/mcp" {
		t.Fatalf("URL patch not applied, got %q", got.URL)
	}
}
