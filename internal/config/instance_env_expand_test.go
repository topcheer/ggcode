package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r139: LoadInstanceConfig previously did a bare typed unmarshal with no
// ${VAR} expansion, so instance config values like `a2a.auth.api_key: ${KEY}`
// stayed literal while the main Load path expanded them
// (config.go: ExpandEnvRecursiveWithLookup). These tests pin the expansion
// chain: process env, instance keys.env (#2293 resolver wiring), the
// literal-preservation fallback for unset vars, and #2284-C explicit-key
// stability across expansion.

func writeInstanceConfigForTest(t *testing.T, workspace, content string) string {
	t.Helper()
	path := InstanceConfigPath(workspace)
	if path == "" {
		t.Fatal("InstanceConfigPath returned empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadInstanceConfig_ExpandsEnvRefs(t *testing.T) {
	withTestHome(t)
	workspace := t.TempDir()
	t.Setenv("R139_TEST_TOK", "tok123")
	t.Setenv("R139_TEST_LANG", "zh")
	path := writeInstanceConfigForTest(t, workspace,
		"a2a:\n  auth:\n    api_key: ${R139_TEST_TOK}\nlanguage: ${R139_TEST_LANG}\n")

	cfg := LoadInstanceConfig(workspace)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	if cfg.A2A.Auth.APIKey != "tok123" {
		t.Errorf("A2A.Auth.APIKey = %q, want expanded %q", cfg.A2A.Auth.APIKey, "tok123")
	}
	if cfg.Language != "zh" {
		t.Errorf("Language = %q, want expanded %q", cfg.Language, "zh")
	}
	// Load must stay read-only: the ${VAR} reference stays on disk (parity
	// with the main config path, where Save re-augments via key migration).
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "${R139_TEST_TOK}") {
		t.Errorf("instance file was rewritten with the expanded value: %s", onDisk)
	}
}

func TestLoadInstanceConfig_InstanceKeysEnvResolution(t *testing.T) {
	withTestHome(t)
	workspace := t.TempDir()
	// R139_TEST_IKEY must NOT exist in the process env for this test:
	// loadKeysEnvInto skips names already set non-empty in os.Environ.
	if v, ok := os.LookupEnv("R139_TEST_IKEY"); ok && v != "" {
		t.Skipf("R139_TEST_IKEY already set in process env (%q)", v)
	}
	dir := InstanceDir(workspace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keys.env"), []byte("R139_TEST_IKEY=instkey\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Restore the process-wide instance resolver map after the test.
	t.Cleanup(func() { _ = LoadInstanceKeysEnv(t.TempDir()) })

	writeInstanceConfigForTest(t, workspace, "a2a:\n  auth:\n    api_key: ${R139_TEST_IKEY}\n")

	// #2293 wiring, end to end: instance keys.env -> resolver map ->
	// ${VAR} expansion inside LoadInstanceConfig.
	if err := LoadInstanceKeysEnv(dir); err != nil {
		t.Fatal(err)
	}
	cfg := LoadInstanceConfig(workspace)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	if cfg.A2A.Auth.APIKey != "instkey" {
		t.Errorf("A2A.Auth.APIKey = %q, want %q resolved from instance keys.env", cfg.A2A.Auth.APIKey, "instkey")
	}
}

func TestLoadInstanceConfig_UnsetVarPreservedLiteral(t *testing.T) {
	withTestHome(t)
	workspace := t.TempDir()
	writeInstanceConfigForTest(t, workspace,
		"a2a:\n  auth:\n    api_key: ${R139_TEST_UNSET_XYZ}\n")

	cfg := LoadInstanceConfig(workspace)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	// ExpandEnvWithLookup contract: unset plain ${VAR} keeps the pattern.
	if cfg.A2A.Auth.APIKey != "${R139_TEST_UNSET_XYZ}" {
		t.Errorf("A2A.Auth.APIKey = %q, want literal preserved", cfg.A2A.Auth.APIKey)
	}
}

func TestLoadInstanceConfig_ExplicitKeysStableAcrossExpansion(t *testing.T) {
	withTestHome(t)
	workspace := t.TempDir()
	t.Setenv("R139_TEST_MI", "42")
	writeInstanceConfigForTest(t, workspace,
		"max_iterations: ${R139_TEST_MI}\na2a:\n  port: 9999\n")

	cfg := LoadInstanceConfig(workspace)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	if cfg.MaxIterations != 42 {
		t.Errorf("MaxIterations = %d, want expanded 42 (typed re-unmarshal)", cfg.MaxIterations)
	}
	// #2284-C explicit-key gate must be unaffected: key paths are invariant
	// under value expansion.
	if !cfg.explicitKeys["max_iterations"] {
		t.Error("explicitKeys missing \"max_iterations\" after expansion")
	}
	if !cfg.explicitKeys["a2a.port"] {
		t.Error("explicitKeys missing dotted path \"a2a.port\"")
	}
}

func TestLoadWithInstance_ExpandsInstanceEnvRefs(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	t.Setenv("R139_TEST_TOK2", "tok456")
	writeInstanceConfigForTest(t, workspace,
		"a2a:\n  auth:\n    api_key: ${R139_TEST_TOK2}\n")

	// Full LoadWithInstance chain: the instance value must merge expanded,
	// not as a literal ${VAR} string shadowing the global value.
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.A2A.Auth.APIKey != "tok456" {
		t.Errorf("A2A.Auth.APIKey = %q, want expanded %q after MergeInstance", cfg.A2A.Auth.APIKey, "tok456")
	}
}
