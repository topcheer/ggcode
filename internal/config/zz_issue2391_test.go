package config

import "testing"

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

	for _, ty := range []string{"http", "sse"} {
		p := patchMCPServerConfig(base, MCPServerConfig{Type: ty, URL: "https://h"})
		if p.Command != "" || p.Args != nil {
			t.Fatalf("stdio→%s kept stdio fields", ty)
		}
	}

	same := patchMCPServerConfig(base, MCPServerConfig{Type: "stdio", Command: "other"})
	if same.Command != "other" || len(same.Args) != 2 {
		t.Fatalf("same-type patch clobbered fields: %+v", same)
	}
}
