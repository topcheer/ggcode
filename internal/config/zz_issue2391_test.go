package config

import "testing"

// TestPatchMCPServerTypeSwitchClearsWS verifies #2391's ws edge: switching
// a server from stdio to ws must clear the stdio-only fields. ws is
// URL-shaped like http/sse, but the type-switch only matched http/sse, so a
// stdio→ws switch resurrected dead Command/Args in the yaml (AddMCPServer's
// doc accepts type "ws").
func TestPatchMCPServerTypeSwitchClearsWS(t *testing.T) {
	base := MCPServerConfig{
		Name:    "s",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "some-server"},
	}
	patched := patchMCPServerConfig(base, MCPServerConfig{Type: "ws", URL: "ws://h/ws"})
	if patched.Command != "" || patched.Args != nil {
		t.Fatalf("stdio→ws kept stdio fields: command=%q args=%v", patched.Command, patched.Args)
	}
	if patched.URL != "ws://h/ws" {
		t.Fatalf("URL not patched: %q", patched.URL)
	}
	// Control: the pre-existing http/sse branches still clear too.
	for _, ty := range []string{"http", "sse"} {
		p := patchMCPServerConfig(base, MCPServerConfig{Type: ty, URL: "https://h"})
		if p.Command != "" || p.Args != nil {
			t.Fatalf("stdio→%s kept stdio fields", ty)
		}
	}
	// And stdio→stdio (no type change) keeps the fields.
	same := patchMCPServerConfig(base, MCPServerConfig{Type: "stdio", Command: "other"})
	if same.Command != "other" || len(same.Args) != 2 {
		t.Fatalf("same-type patch clobbered fields: %+v", same)
	}
}
