package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPlaintextMigrationKeepsLegacyA2AKey(t *testing.T) {
	withTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: openai-compatible
endpoint: local
model: test-model
a2a:
  enabled: true
  api_key: "legacy-a2a-secret-123"
vendors:
  openai-compatible:
    api_key: sk-plainkey-456
    endpoints:
      local:
        protocol: openai
        base_url: https://example.test/v1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.A2A.Auth.APIKey; got != "legacy-a2a-secret-123" {
		t.Fatalf("#4881 regression: legacy a2a api_key lost across the plaintext-migration reload - A2A server would run with NO auth this session; cfg.A2A.Auth.APIKey = %q, want %q", got, "legacy-a2a-secret-123")
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got := reloaded.A2A.Auth.APIKey; got != "legacy-a2a-secret-123" {
		t.Fatalf("second Load lost the legacy a2a key via the other code path: got %q", got)
	}
}

func TestMigrateLegacyA2AKeySurvivesReloadShape(t *testing.T) {
	raw := map[string]interface{}{
		"a2a": map[string]interface{}{
			"enabled": true,
			"api_key": "k",
		},
	}
	migrateLegacyA2AAPIKey(raw)
	a2a, ok := raw["a2a"].(map[string]interface{})
	if !ok {
		t.Fatalf("a2a block missing or wrong type: %T", raw["a2a"])
	}
	if _, still := a2a["api_key"]; still {
		t.Fatal("legacy top-level a2a.api_key must be deleted after migration")
	}
	auth, ok := a2a["auth"].(map[string]interface{})
	if !ok {
		t.Fatal("a2a.auth block missing after migration")
	}
	if auth["api_key"] != "k" {
		t.Fatalf("a2a.auth.api_key = %v, want k", auth["api_key"])
	}
}
