package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r137 audit regression: instance-scope migration must not push
// GGCODE_I_{hash}_* names into the process environment (#2293).
func TestInstanceMigrationKeepsKeysOutOfProcessEnv(t *testing.T) {
	tmpDir := t.TempDir()
	instDir := filepath.Join(tmpDir, "inst")
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte("vendors:\n  openai:\n    api_key: sk-inst-137\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hash := "abc137"
	findings, err := MigrateInstancePlaintextAPIKeys(instPath, hash)
	if err != nil {
		t.Fatalf("MigrateInstancePlaintextAPIKeys error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	envVar := findings[0].EnvVar
	if !strings.HasPrefix(envVar, instanceEnvVarPrefix+hash+"_") {
		t.Fatalf("unexpected instance env var name: %q", envVar)
	}
	if v, exists := os.LookupEnv(envVar); exists {
		t.Errorf("instance migration leaked %s=%q into process env", envVar, v)
	}

	// The value still lands in the instance keys.env and the YAML is rewritten.
	instKeys, err := os.ReadFile(filepath.Join(instDir, "keys.env"))
	if err != nil {
		t.Fatalf("instance keys.env missing: %v", err)
	}
	if !strings.Contains(string(instKeys), envVar+"='sk-inst-137'") {
		t.Errorf("instance keys.env missing migrated value:\n%s", string(instKeys))
	}
	instYAML, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(instYAML), "${"+envVar+"}") {
		t.Errorf("instance YAML not rewritten to env reference:\n%s", string(instYAML))
	}
}

// r137 audit regression: a key rotated by another process (a CLI session
// updates the instance keys.env) must be picked up by LoadInstanceKeysEnv
// even when this process migrated the same key earlier. The old migration
// os.Setenv made loadKeysEnvInto skip the fresh entry forever, pinning the
// stale value in this session's resolver map.
func TestInstanceKeysRotationSurvivesInProcessMigration(t *testing.T) {
	tmpDir := t.TempDir()
	instDir := filepath.Join(tmpDir, "inst")
	if err := os.MkdirAll(instDir, 0755); err != nil {
		t.Fatal(err)
	}
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte("vendors:\n  openai:\n    api_key: sk-old-137\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hash := "abc137"
	findings, err := MigrateInstancePlaintextAPIKeys(instPath, hash)
	if err != nil {
		t.Fatalf("migrate error: %v", err)
	}
	envVar := findings[0].EnvVar
	t.Cleanup(func() {
		os.Unsetenv(envVar)
		empty := map[string]string{}
		instanceKeysEnvPointer.Store(&empty)
	})

	// Another process rotates the key: instance keys.env now holds sk-new-137.
	rotated := "# Managed by ggcode\nexport " + envVar + "='sk-new-137'\n"
	if err := os.WriteFile(filepath.Join(instDir, "keys.env"), []byte(rotated), 0600); err != nil {
		t.Fatalf("write rotated keys.env: %v", err)
	}

	// Long-running session reloads instance keys (LoadWithInstance path).
	if err := LoadInstanceKeysEnv(instDir); err != nil {
		t.Fatalf("LoadInstanceKeysEnv error: %v", err)
	}
	p := instanceKeysEnvPointer.Load()
	got := "<nil map>"
	if p != nil {
		got = (*p)[envVar]
	}
	if got != "sk-new-137" {
		t.Fatalf("rotated key not picked up: resolver map has %q, want %q", got, "sk-new-137")
	}
}

// Global-scope migration keeps its existing process-env behavior.
func TestGlobalMigrationStillSetsProcessEnv(t *testing.T) {
	tmpDir := t.TempDir()
	keysEnvPathOverride = filepath.Join(tmpDir, "keys.env")
	defer func() { keysEnvPathOverride = "" }()

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("vendors:\n  vend137:\n    api_key: sk-global-137\n"), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := MigratePlaintextAPIKeys(cfgPath)
	if err != nil {
		t.Fatalf("MigratePlaintextAPIKeys error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	envVar := findings[0].EnvVar
	t.Cleanup(func() { os.Unsetenv(envVar) })
	if os.Getenv(envVar) != "sk-global-137" {
		t.Errorf("global migration should set process env %s, got %q", envVar, os.Getenv(envVar))
	}
}
