package config

import (
	"os"
	"path/filepath"
	"testing"
)

// C: clearing an int back to the global default must reach the delta.
func TestIssue1519C_ClearToDefaultReachesDelta(t *testing.T) {
	var c Config
	delta := map[string]interface{}{}
	c.diffInt("max_iterations", 0, 80, delta)
	if v, ok := delta["max_iterations"]; !ok || v != 0 {
		t.Fatalf("reset-to-0 must be recorded, got %v %v", delta, ok)
	}
	c.diffScalar("system_prompt", "", "old prompt", delta)
	if v, ok := delta["system_prompt"]; !ok || v != "" {
		t.Fatalf("clear-to-empty must be recorded, got %v %v", delta, ok)
	}

	quiet := map[string]interface{}{}
	c.diffInt("max_iterations", 80, 80, quiet)
	if _, ok := quiet["max_iterations"]; ok {
		t.Fatalf("unchanged value must not enter delta: %v", quiet)
	}
}

// B: after a successful migration the legacy file is gone.
func TestIssue1519B_MigrationRemovesLegacy(t *testing.T) {
	ws := t.TempDir()
	legacyDir := filepath.Join(ws, ".ggcode")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "a2a.yaml")
	if err := os.WriteFile(legacy, []byte("port: 7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", ws)
	if !MigrateA2AYaml(ws) {
		t.Fatal("migration must succeed")
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy file must be removed after migration, stat err=%v", err)
	}
	if !HasInstanceConfig(ws) {
		t.Fatal("instance config must exist after migration")
	}
}
