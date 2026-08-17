package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue609InstanceSubconfigsDoNotLeakToGlobal is the regression test for
// #609: mergeKnightConfig / mergeA2AConfigFields / mergeSubAgentConfig /
// mergeSwarmConfig did not register their top-level YAML keys (knight, a2a,
// subagents, swarm) in instanceFields, so instance-only values leaked into
// the global ggcode.yaml on Save(). The tracked-map mechanism is the same one
// that fixed the hook leak (mergeHookConfig).
func TestIssue609InstanceSubconfigsDoNotLeakToGlobal(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := filepath.Join(cfgDir, "ggcode.yaml")
	globalYAML := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: globalkey609
    endpoints:
      main:
        protocol: openai
        base_url: https://global.example.com/v1
`
	if err := os.WriteFile(cfgPath, []byte(globalYAML), 0600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	instDir := InstanceDir(ws)
	if instDir == "" {
		t.Fatal("InstanceDir returned empty")
	}
	if err := os.MkdirAll(instDir, 0700); err != nil {
		t.Fatal(err)
	}
	instYAML := `
knight:
  trust_level: high
subagents:
  max_concurrent: 7
swarm:
  inbox_size: 64
a2a:
  port: 4545
`
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.WriteFile(instPath, []byte(instYAML), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWithInstance(cfgPath, ws)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Sanity: the instance values are merged into memory.
	if cfg.KnightConfig.TrustLevel != "high" {
		t.Fatalf("knight.trust_level should be merged in memory, got %q", cfg.KnightConfig.TrustLevel)
	}
	if cfg.SubAgents.MaxConcurrent != 7 {
		t.Fatalf("subagents.max_concurrent should be merged in memory, got %d", cfg.SubAgents.MaxConcurrent)
	}
	if cfg.Swarm.InboxSize != 64 {
		t.Fatalf("swarm.inbox_size should be merged in memory, got %d", cfg.Swarm.InboxSize)
	}
	if cfg.A2A.Port != 4545 {
		t.Fatalf("a2a.port should be merged in memory, got %d", cfg.A2A.Port)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	global, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading global config: %v", err)
	}
	for _, leaked := range []string{"trust_level", "max_concurrent", "inbox_size", "4545"} {
		if strings.Contains(string(global), leaked) {
			t.Errorf("instance key %q leaked into global ggcode.yaml (#609). file:\n%s", leaked, global)
		}
	}

	// The values must persist in the instance file instead.
	inst, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	for _, want := range []string{"trust_level", "max_concurrent", "inbox_size", "4545"} {
		if !strings.Contains(string(inst), want) {
			t.Errorf("instance key %q should remain in the instance file. file:\n%s", want, inst)
		}
	}
}

// TestIssue609InstanceFieldsRegistered: MergeInstance must register knight /
// a2a / subagents / swarm in instanceFields whenever instance values fill
// global gaps (the tracking is what Save() strips from the global write).
func TestIssue609InstanceFieldsRegistered(t *testing.T) {
	global := &Config{}
	instance := &Config{
		KnightConfig: KnightConfig{TrustLevel: "high"},
		A2A:          A2AConfig{MaxTasks: 11},
		SubAgents:    SubAgentConfig{MaxConcurrent: 7},
		Swarm:        SwarmConfig{InboxSize: 64},
	}
	MergeInstance(global, instance)

	fields := global.InstanceFields()
	have := map[string]bool{}
	for _, f := range fields {
		have[f] = true
	}
	for _, key := range []string{"knight", "a2a", "subagents", "swarm"} {
		if !have[key] {
			t.Errorf("instanceFields missing %q after MergeInstance; got %v", key, fields)
		}
	}
}
