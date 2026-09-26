package config

import (
	"os"
	"path/filepath"
	"testing"
)

// r138: legacy .ggcode/a2a.yaml vs instance-config a2a section ownership.
// MigrateA2AYaml permanently skips migration whenever an instance config
// already exists, so a legacy override file can coexist with an instance a2a
// section forever. The old unconditional MergeA2AConfig override re-stomped
// those fields on every load, making A2A values written through the instance
// config silently dead. LoadWithInstance now gates each legacy field on the
// instance explicit-key set (#2284-C).

// Bug repro: fields explicitly set in the instance a2a section must win over
// the stale legacy file (previously the legacy file always won).
func TestLoadWithInstance_LegacyA2AGatedByInstanceExplicit(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("port: 8080\napi_key: legacy-key\n"), 0644)

	// Instance config explicitly owns port and auth.api_key.
	os.MkdirAll(filepath.Dir(InstanceConfigPath(workspace)), 0755)
	os.WriteFile(InstanceConfigPath(workspace),
		[]byte("a2a:\n  port: 9090\n  auth:\n    api_key: inst-key\n"), 0644)

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.A2A.Port != 9090 {
		t.Errorf("A2A.Port = %d, want 9090 (instance-explicit wins over legacy a2a.yaml 8080)", cfg.A2A.Port)
	}
	if cfg.A2A.Auth.APIKey != "inst-key" {
		t.Errorf("A2A.Auth.APIKey = %q, want %q (instance-explicit wins over legacy)", cfg.A2A.Auth.APIKey, "inst-key")
	}
}

// Explicit clears in the instance config must not be resurrected by the
// legacy file's #665 re-enable path.
func TestLoadWithInstance_LegacyA2ACannotReEnableInstanceDisabled(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("disabled: false\n"), 0644)
	os.MkdirAll(filepath.Dir(InstanceConfigPath(workspace)), 0755)
	os.WriteFile(InstanceConfigPath(workspace),
		[]byte("a2a:\n  disabled: true\n"), 0644)

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.A2A.Disabled {
		t.Error("A2A.Disabled = false, want true (legacy a2a.yaml disabled:false must not re-enable an instance-explicit disable)")
	}
}

// Backward compat: when the instance config does NOT set an a2a field, the
// legacy file still fills it in (gap-filler semantics preserved per field).
func TestLoadWithInstance_LegacyA2AFillsFieldsInstanceDoesNotOwn(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("port: 8080\nmax_tasks: 3\n"), 0644)
	// Instance config exists and even has an a2a section, but only owns port.
	os.MkdirAll(filepath.Dir(InstanceConfigPath(workspace)), 0755)
	os.WriteFile(InstanceConfigPath(workspace),
		[]byte("a2a:\n  port: 9090\n"), 0644)

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.A2A.Port != 9090 {
		t.Errorf("A2A.Port = %d, want 9090 (instance-explicit)", cfg.A2A.Port)
	}
	if cfg.A2A.MaxTasks != 3 {
		t.Errorf("A2A.MaxTasks = %d, want 3 (legacy gap-fill for field instance does not own)", cfg.A2A.MaxTasks)
	}
}

// No instance config at all: legacy override behavior is fully unchanged.
func TestLoadWithInstance_LegacyA2AWithoutInstanceConfigUnchanged(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("port: 8080\napi_key: legacy-key\n"), 0644)

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.A2A.Port != 8080 || cfg.A2A.Auth.APIKey != "legacy-key" {
		t.Errorf("A2A = {port:%d key:%q}, want legacy values applied unconditionally (no instance config)",
			cfg.A2A.Port, cfg.A2A.Auth.APIKey)
	}
}

// MigrateA2AYaml must not drop the legacy flat "api_key" form: the wrapped
// a2a section has no top-level api_key field, so pre-r138 the key silently
// vanished and the workspace's A2A auth broke after migration.
func TestMigrateA2AYaml_FlatAPIKeyPreserved(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("api_key: flat-key\nport: 8080\n"), 0644)

	if !MigrateA2AYaml(workspace) {
		t.Fatal("MigrateA2AYaml should return true on successful migration")
	}
	inst := LoadInstanceConfig(workspace)
	if inst == nil {
		t.Fatal("instance config should exist after migration")
	}
	if inst.A2A.Auth.APIKey != "flat-key" {
		t.Errorf("A2A.Auth.APIKey = %q, want %q (flat api_key must move to auth.api_key)",
			inst.A2A.Auth.APIKey, "flat-key")
	}
	if inst.A2A.Port != 8080 {
		t.Errorf("A2A.Port = %d, want 8080", inst.A2A.Port)
	}
}

// Unit-level: a nil gate keeps MergeA2AConfig's historical unconditional
// behavior, and a gate that claims everything suppresses every field.
func TestMergeA2AConfigWithGate(t *testing.T) {
	base := &A2AConfig{Port: 1, MaxTasks: 1, TaskTimeout: "1m"}
	base.Auth.APIKey = "base"
	override := &A2AConfig{Port: 2, MaxTasks: 2, TaskTimeout: "2m", Disabled: true, disabledExplicit: true}
	override.Auth.APIKey = "over"

	MergeA2AConfigWithGate(base, override, nil)
	if base.Port != 2 || base.MaxTasks != 2 || base.TaskTimeout != "2m" ||
		base.Auth.APIKey != "over" || !base.Disabled {
		t.Fatal("nil gate must reproduce MergeA2AConfig's unconditional override")
	}

	base2 := &A2AConfig{Port: 1, MaxTasks: 1, TaskTimeout: "1m"}
	base2.Auth.APIKey = "base"
	override2 := &A2AConfig{Port: 2, MaxTasks: 2, TaskTimeout: "2m", Disabled: true, disabledExplicit: true}
	override2.Auth.APIKey = "over"
	all := func(string) bool { return true }
	MergeA2AConfigWithGate(base2, override2, all)
	if base2.Port != 1 || base2.MaxTasks != 1 || base2.TaskTimeout != "1m" ||
		base2.Auth.APIKey != "base" || base2.Disabled {
		t.Fatal("all-gated override must not touch any field")
	}
}
