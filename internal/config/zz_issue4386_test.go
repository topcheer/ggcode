package config

// #4386: Command/URL are strings, so the patch layer's "empty = not
// provided" semantics left the form no way to clear them - a cleared field
// saved "successfully" while the old command/url silently resurrected. The
// CommandClear/URLClear sentinels mirror #979's env_clear / #1434-B's
// args_clear (non-nil empties, possible because those fields are
// maps/slices).

import "testing"

func TestIssue4386_CommandClearSentinel(t *testing.T) {
	base := MCPServerConfig{Name: "s", Type: "stdio", Command: "npx", URL: "https://old"}

	merged := patchMCPServerConfig(base, MCPServerConfig{CommandClear: true})
	if merged.Command != "" {
		t.Fatalf("CommandClear must clear command, got %q (#4386)", merged.Command)
	}
	if merged.URL != "https://old" {
		t.Fatalf("CommandClear must not touch the other side, url = %q", merged.URL)
	}
}

func TestIssue4386_URLClearSentinel(t *testing.T) {
	base := MCPServerConfig{Name: "s", Type: "http", Command: "npx", URL: "https://old"}

	merged := patchMCPServerConfig(base, MCPServerConfig{URLClear: true})
	if merged.URL != "" {
		t.Fatalf("URLClear must clear url, got %q (#4386)", merged.URL)
	}
	if merged.Command != "npx" {
		t.Fatalf("URLClear must not touch command, got %q", merged.Command)
	}
}

func TestIssue4386_PlainPatchUnchanged(t *testing.T) {
	base := MCPServerConfig{Name: "s", Type: "http", URL: "https://old"}

	merged := patchMCPServerConfig(base, MCPServerConfig{URL: "https://new"})
	if merged.URL != "https://new" {
		t.Fatalf("plain url patch must still override, got %q", merged.URL)
	}

	kept := patchMCPServerConfig(base, MCPServerConfig{Name: "s"})
	if kept.URL != "https://old" {
		t.Fatalf("empty patch must still keep the old value, got %q", kept.URL)
	}
}

func TestIssue4386_UpsertMCPServerClearRoundTrip(t *testing.T) {
	c := &Config{MCPServers: []MCPServerConfig{{Name: "s", Type: "http", URL: "https://old"}}}
	c.UpsertMCPServer(MCPServerConfig{Name: "s", Type: "http", URLClear: true})

	for _, s := range c.MCPServers {
		if s.Name != "s" {
			continue
		}
		if s.URL != "" {
			t.Fatalf("upsert with URLClear must persist an empty url, got %q (#4386)", s.URL)
		}
		return
	}
	t.Fatalf("server s vanished from the list")
}
