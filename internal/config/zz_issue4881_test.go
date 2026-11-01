package config

import (
	"os"
	"path/filepath"
	"testing"
)

// zz_issue4881_test.go pins the fix for #4881: Load discards the legacy
// a2a.api_key migration when the plaintext-key migration rewrites the file.
//
// Regression shape: migrateLegacyA2AAPIKey(raw) at the top of Load mutates the
// in-memory raw map only; MigratePlaintextAPIKeys(path) then rewrites the disk
// file from its own disk-based raw copy (a2a section untouched), and the
// migrated>0 reload path wholly replaced raw with the DISK content — silently
// discarding the legacy migration. A2AConfig has no top-level legacy api_key
// field, so cfg.A2A.Auth.APIKey ended up empty and the A2A server ran with NO
// auth for the whole first session after upgrade (fail-open).
//
// The fix re-runs migrateLegacyA2AAPIKey on the reloaded raw map.

// TestLoadPlaintextMigrationKeepsLegacyA2AKey: a config carrying BOTH a legacy
// a2a.api_key and a plaintext vendor key (the exact trigger — the vendor key
// makes MigratePlaintextAPIKeys rewrite the file, activating the reload path)
// must still yield a populated A2A auth key after Load.
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

	// Second Load: plaintext keys are gone from disk (now ${VAR} refs), so the
	// reload path must NOT trigger; the pre-reload migration path applies and
	// the key must still be there (no flip-flop between the two paths).
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if got := reloaded.A2A.Auth.APIKey; got != "legacy-a2a-secret-123" {
		t.Fatalf("second Load lost the legacy a2a key via the other code path: got %q", got)
	}
}

// TestMigrateLegacyA2AKeySurvivesReloadShape: unit-pins the reload shape
// itself — a freshly disk-read raw map (legacy a2a.api_key present, no
// auth block) passed through the fix's re-run must come out with the key
// under a2a.auth.api_key and the legacy top-level key gone.
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
