package config

import (
	"os"
	"path/filepath"
	"testing"
)

// load2702 writes an instance yaml and loads it through the real path so
// explicitKeys (dotted, #2702) is populated exactly as in production.
func load2702(t *testing.T, yaml string) *Config {
	t.Helper()
	withTestHome(t)
	ws := filepath.Join(t.TempDir(), "project")
	instDir := InstanceDir(ws)
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadInstanceConfig(ws)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	return cfg
}

// #2702 scenario 1: instance clears a2a.auth.api_key to "" explicitly.
// The write side (diffA2A) persists the clear; the old gap-fill read side
// ignored the empty value and the global secret resurrected on reload.
func TestIssue2702_A2AAPIKeyClearSurvivesReload(t *testing.T) {
	instance := load2702(t, "a2a:\n  auth:\n    api_key: \"\"\n")
	global := &Config{A2A: A2AConfig{Auth: A2AAuthConfig{APIKey: "secret"}}}

	MergeInstance(global, instance)

	if global.A2A.Auth.APIKey != "" {
		t.Fatalf("explicit instance api_key clear resurrected global secret on reload: got %q (#2702)", global.A2A.Auth.APIKey)
	}
}

// #2702 scenario 2: instance disables knight explicitly (enabled: false
// with global true). The old merge only accepted the false->true direction.
func TestIssue2702_KnightDisableSurvivesReload(t *testing.T) {
	instance := load2702(t, "knight:\n  enabled: false\n")
	global := &Config{KnightConfig: KnightConfig{Enabled: true}}

	MergeInstance(global, instance)

	if global.KnightConfig.Enabled {
		t.Fatal("explicit instance knight.enabled=false resurrected global true on reload (#2702)")
	}
}

// Explicit-true keeps working (existing direction), and absent keys keep the
// gap-fill semantics: no a2a section at all -> global value untouched.
func TestIssue2702_GapFillSemanticsUnchanged(t *testing.T) {
	instance := load2702(t, "language: zh-CN\n")
	global := &Config{A2A: A2AConfig{Port: 9000}, KnightConfig: KnightConfig{Enabled: true}}

	MergeInstance(global, instance)

	if global.A2A.Port != 9000 {
		t.Errorf("global a2a.port must stay when instance has no a2a section, got %d", global.A2A.Port)
	}
	if !global.KnightConfig.Enabled {
		t.Error("global knight.enabled must stay when instance has no knight section")
	}

	// Explicit true override in the other direction also works.
	instanceTrue := load2702(t, "knight:\n  enabled: true\n")
	globalFalse := &Config{KnightConfig: KnightConfig{Enabled: false}}
	MergeInstance(globalFalse, instanceTrue)
	if !globalFalse.KnightConfig.Enabled {
		t.Error("explicit instance knight.enabled=true must override global false (#2702 symmetric)")
	}
}

// Scalar clears within a2a (port: 0) honor the explicit rule too.
func TestIssue2702_A2AScalarClears(t *testing.T) {
	instance := load2702(t, "a2a:\n  port: 0\n  host: \"\"\n  max_tasks: 0\n")
	global := &Config{A2A: A2AConfig{Port: 4747, Host: "example.com", MaxTasks: 16}}

	MergeInstance(global, instance)

	if global.A2A.Port != 0 || global.A2A.Host != "" || global.A2A.MaxTasks != 0 {
		t.Fatalf("explicit a2a scalar clears lost on reload: port=%d host=%q max=%d (#2702)", global.A2A.Port, global.A2A.Host, global.A2A.MaxTasks)
	}
}

// The dotted explicit set must not break the pre-existing top-level scalar
// lookups (#2284-C semantics stay intact).
func TestIssue2702_TopLevelExplicitUnchanged(t *testing.T) {
	instance := load2702(t, "max_iterations: 0\n")
	global := &Config{MaxIterations: 80}

	MergeInstance(global, instance)

	if global.MaxIterations != 0 {
		t.Fatalf("top-level explicit max_iterations=0 clear lost: got %d (#2284-C regression)", global.MaxIterations)
	}
}
