package config

// #1515 case A: .ggcode/a2a.yaml is the ONE config surface that did not
// expand ${VAR} - auth.api_key merged as the literal "${A2A_KEY}" and
// auth failed silently. Both the flat api_key form and the nested
// auth.api_key form must resolve like the main config does.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeA2AOverride1515(t *testing.T, content string) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".ggcode", "a2a.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestIssue1515NestedAuthKeyExpands(t *testing.T) {
	t.Setenv("A2A_KEY_1515", "sk-resolved-1515")
	ws := writeA2AOverride1515(t, "auth:\n  api_key: ${A2A_KEY_1515}\n")
	ov := LoadA2AOverride(ws)
	if ov == nil {
		t.Fatal("override must load")
	}
	if ov.Auth.APIKey != "sk-resolved-1515" {
		t.Fatalf("nested auth.api_key must expand, got %q", ov.Auth.APIKey)
	}
}

func TestIssue1515FlatLegacyKeyExpands(t *testing.T) {
	t.Setenv("A2A_KEY_1515", "sk-flat-1515")
	ws := writeA2AOverride1515(t, "api_key: ${A2A_KEY_1515}\n")
	ov := LoadA2AOverride(ws)
	if ov == nil {
		t.Fatal("override must load")
	}
	if ov.Auth.APIKey != "sk-flat-1515" {
		t.Fatalf("flat legacy api_key must expand after migration, got %q", ov.Auth.APIKey)
	}
}
