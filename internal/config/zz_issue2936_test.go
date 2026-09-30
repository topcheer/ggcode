package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestIssue2936NestedZeroResetOverwritesStaleDiskValue: a nested override
// (swarm.max_teammates_per_team: 30) followed by a reset to 0 ("use the
// global default") must reach the instance file as an explicit 0. The old
// zero-value suppression dropped the key from the delta, the deep merge
// kept the stale 30 on disk, and MergeInstance resurrected it on reload.
func TestIssue2936NestedZeroResetOverwritesStaleDiskValue(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := writeGlobalConfig(t, tmpDir)
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Session 1: instance-scope override.
	cfg.Swarm.MaxTeammatesPerTeam = 30
	cfg.Swarm.PollInterval = 45 * time.Second
	if err := cfg.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped (override): %v", err)
	}
	instPath := filepath.Join(InstanceDir(workspace), "ggcode.yaml")
	instData, err := os.ReadFile(instPath)
	if err != nil {
		t.Fatalf("reading instance config: %v", err)
	}
	if !strings.Contains(string(instData), "max_teammates_per_team: 30") {
		t.Fatalf("override not persisted; got:\n%s", instData)
	}

	// Session 2: reset back to zero-value defaults.
	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg2.Swarm.MaxTeammatesPerTeam != 30 {
		t.Fatalf("precondition: expected stale 30 to resurrect pre-fix, got %d", cfg2.Swarm.MaxTeammatesPerTeam)
	}
	cfg2.Swarm.MaxTeammatesPerTeam = 0
	cfg2.Swarm.PollInterval = 0
	if err := cfg2.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped (reset): %v", err)
	}

	// The reset must survive a reload: explicit 0 in the delta overwrites
	// the stale merged value instead of being silently dropped.
	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload after reset: %v", err)
	}
	if cfg3.Swarm.MaxTeammatesPerTeam != 0 {
		t.Errorf("swarm.max_teammates_per_team resurrected as %d; reset was dropped by zero-value suppression", cfg3.Swarm.MaxTeammatesPerTeam)
	}
	if cfg3.Swarm.PollInterval != 0 {
		t.Errorf("swarm.poll_interval resurrected as %v", cfg3.Swarm.PollInterval)
	}
}

// TestIssue2936NestedResetAllAffectedFields sweeps the 9 fields named in the
// issue through the same override-then-reset cycle at delta level.
func TestIssue2936NestedResetAllAffectedFields(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := writeGlobalConfig(t, tmpDir)
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Set non-zero overrides, save, then reset everything to zero and build
	// the delta: every field must appear as an explicit zero, not be dropped.
	cfg.KnightConfig.TrustLevel = "high"
	cfg.KnightConfig.DailyTokenBudget = 1000
	cfg.KnightConfig.IdleDelaySec = 60
	cfg.SubAgents.MaxConcurrent = 5
	cfg.SubAgents.Timeout = 2 * time.Minute
	cfg.Swarm.MaxTeammatesPerTeam = 30
	cfg.Swarm.TeammateTimeout = 90 * time.Second
	cfg.Swarm.InboxSize = 64
	cfg.Swarm.PollInterval = 45 * time.Second
	if err := cfg.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}

	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cfg2.KnightConfig.TrustLevel = ""
	cfg2.KnightConfig.DailyTokenBudget = 0
	cfg2.KnightConfig.IdleDelaySec = 0
	cfg2.SubAgents.MaxConcurrent = 0
	cfg2.SubAgents.Timeout = 0
	cfg2.Swarm.MaxTeammatesPerTeam = 0
	cfg2.Swarm.TeammateTimeout = 0
	cfg2.Swarm.InboxSize = 0
	cfg2.Swarm.PollInterval = 0
	if err := cfg2.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped (reset): %v", err)
	}

	// With global defaults at zero, current == global after the reset, so
	// the diff omits the keys - the nested nil deletion markers written by
	// applyInstanceResets (#2936) must remove the stale on-disk values
	// instead of letting the deep merge resurrect them.
	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload after reset: %v", err)
	}
	if cfg3.KnightConfig.TrustLevel != "" {
		t.Errorf("knight.trust_level resurrected as %q", cfg3.KnightConfig.TrustLevel)
	}
	if cfg3.KnightConfig.DailyTokenBudget != 0 {
		t.Errorf("knight.daily_token_budget resurrected as %d", cfg3.KnightConfig.DailyTokenBudget)
	}
	if cfg3.KnightConfig.IdleDelaySec != 0 {
		t.Errorf("knight.idle_delay_sec resurrected as %d", cfg3.KnightConfig.IdleDelaySec)
	}
	if cfg3.SubAgents.MaxConcurrent != 0 {
		t.Errorf("subagents.max_concurrent resurrected as %d", cfg3.SubAgents.MaxConcurrent)
	}
	if cfg3.SubAgents.Timeout != 0 {
		t.Errorf("subagents.timeout resurrected as %v", cfg3.SubAgents.Timeout)
	}
	if cfg3.Swarm.MaxTeammatesPerTeam != 0 {
		t.Errorf("swarm.max_teammates_per_team resurrected as %d", cfg3.Swarm.MaxTeammatesPerTeam)
	}
	if cfg3.Swarm.TeammateTimeout != 0 {
		t.Errorf("swarm.teammate_timeout resurrected as %v", cfg3.Swarm.TeammateTimeout)
	}
	if cfg3.Swarm.InboxSize != 0 {
		t.Errorf("swarm.inbox_size resurrected as %d", cfg3.Swarm.InboxSize)
	}
	if cfg3.Swarm.PollInterval != 0 {
		t.Errorf("swarm.poll_interval resurrected as %v", cfg3.Swarm.PollInterval)
	}
}

// TestIssue2936ImpersonationHeaderSwapAndClear: a same-length CustomHeaders
// key swap must be detected (content compare, not length), and clearing the
// headers must produce an empty-map delta that overwrites the stale disk map.
func TestIssue2936ImpersonationHeaderSwapAndClear(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := writeGlobalConfig(t, tmpDir)
	workspace := t.TempDir()

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("LoadWithInstance: %v", err)
	}

	// Same-length swap: {x-a:"1"} -> {x-b:"2"}.
	cfg.Impersonation.CustomHeaders = map[string]string{"x-a": "1"}
	if err := cfg.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped: %v", err)
	}

	cfg2, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg2.Impersonation.CustomHeaders["x-a"] != "1" {
		instData2, _ := os.ReadFile(filepath.Join(InstanceDir(workspace), "ggcode.yaml"))
		t.Fatalf("precondition: initial header not persisted: %#v; instance file:\n%s", cfg2.Impersonation.CustomHeaders, instData2)
	}
	cfg2.Impersonation.CustomHeaders = map[string]string{"x-b": "2"} // same length
	delta := cfg2.marshalInstanceDelta()
	imp, _ := delta["impersonation"].(map[string]interface{})
	if imp == nil {
		t.Fatal("same-length header swap produced no impersonation delta (length-only compare)")
	}
	hdrs, _ := imp["custom_headers"].(map[string]string)
	if hdrs == nil || hdrs["x-b"] != "2" {
		t.Errorf("custom_headers not snapshotted in delta; got: %#v", imp["custom_headers"])
	}

	// Clear path: empty headers must be written, not suppressed.
	cfg2.Impersonation.CustomHeaders = map[string]string{}
	delta2 := cfg2.marshalInstanceDelta()
	imp2, _ := delta2["impersonation"].(map[string]interface{})
	if imp2 == nil {
		t.Fatal("clearing headers produced no impersonation delta")
	}
	hdrs2, ok := imp2["custom_headers"].(map[string]string)
	if !ok || len(hdrs2) != 0 {
		t.Errorf("cleared custom_headers must be an explicit empty map; got %#v", imp2["custom_headers"])
	}

	// End to end: the clear must survive a reload (wholesale map replace
	// beats the key-merge in deepMergeYAMLMaps because map[string]string
	// fails the map[string]interface{} assertion).
	if err := cfg2.SaveInstanceScoped(workspace); err != nil {
		t.Fatalf("SaveInstanceScoped (clear): %v", err)
	}
	cfg3, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatalf("reload after clear: %v", err)
	}
	if got := cfg3.Impersonation.CustomHeaders["x-a"]; got != "" {
		t.Errorf("stale header x-a resurrected with value %q after clear", got)
	}
}
