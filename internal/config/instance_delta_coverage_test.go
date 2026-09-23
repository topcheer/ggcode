package config

import (
	"testing"
	"time"
)

// --- instance_delta.go diff family (was 33-58%) ---
//
// These functions decide which instance-scope keys are persisted when an
// instance config differs from the global snapshot. A missed delta means a
// user override is silently lost on save (#524-class regressions).

func TestDiffStringSlice(t *testing.T) {
	cfg := &Config{}

	// Empty current: "use global default" must NOT be persisted (sa-137 note).
	delta := map[string]interface{}{}
	cfg.diffStringSlice("interfaces", nil, []string{"en0"}, delta)
	if _, ok := delta["interfaces"]; ok {
		t.Error("empty current should not produce a delta")
	}

	// Length differs → delta.
	delta = map[string]interface{}{}
	cfg.diffStringSlice("interfaces", []string{"en0", "en1"}, []string{"en0"}, delta)
	if got, ok := delta["interfaces"].([]string); !ok || len(got) != 2 {
		t.Errorf("length-differs case: got %v", delta["interfaces"])
	}

	// Same length, different content → delta.
	delta = map[string]interface{}{}
	cfg.diffStringSlice("interfaces", []string{"en1", "en0"}, []string{"en0", "en1"}, delta)
	if _, ok := delta["interfaces"]; !ok {
		t.Error("reordered content should produce a delta")
	}

	// Identical → no delta.
	delta = map[string]interface{}{}
	cfg.diffStringSlice("interfaces", []string{"en0", "en1"}, []string{"en0", "en1"}, delta)
	if _, ok := delta["interfaces"]; ok {
		t.Error("identical slices should not produce a delta")
	}
}

func TestDiffIM(t *testing.T) {
	cfg := &Config{}

	// Identical → no im section.
	delta := map[string]interface{}{}
	cfg.diffIM(&IMConfig{}, &IMConfig{}, delta)
	if _, ok := delta["im"]; ok {
		t.Fatalf("identical configs should not produce im delta: %v", delta)
	}

	cur := &IMConfig{}
	glo := &IMConfig{}
	cur.Enabled = true
	cur.ActiveSessionPolicy = "steal"
	requireLocal := true
	cur.RequireLocalSession = &requireLocal
	cur.OutputMode = "stream"
	cur.Streaming.Enabled = true
	cur.STT.Provider = "whisper"
	cur.Adapters = map[string]IMAdapterConfig{
		"qq": {Platform: "qq", Enabled: true},
	}

	delta = map[string]interface{}{}
	cfg.diffIM(cur, glo, delta)
	im, ok := delta["im"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected im delta, got %v", delta)
	}
	for _, key := range []string{"enabled", "active_session_policy", "require_local_session", "output_mode", "streaming", "stt"} {
		if _, ok := im[key]; !ok {
			t.Errorf("im delta missing %q: %v", key, im)
		}
	}
	adapters, ok := im["adapters"].(map[string]interface{})
	if !ok {
		t.Fatalf("im delta missing adapters: %v", im)
	}
	if adapters["qq"] == nil {
		t.Errorf("adapter 'qq' missing from delta: %v", adapters)
	}

	// Adapter already in global must NOT be re-persisted.
	delta = map[string]interface{}{}
	glo2 := &IMConfig{}
	glo2.Adapters = map[string]IMAdapterConfig{"qq": {}}
	cfg.diffIM(&IMConfig{}, glo2, delta)
	if im, ok := delta["im"].(map[string]interface{}); ok {
		if _, has := im["adapters"]; has {
			t.Errorf("adapter present in global should not be in delta: %v", im)
		}
	}
}

func TestDiffA2A(t *testing.T) {
	cfg := &Config{}

	// Identical → no a2a section.
	delta := map[string]interface{}{}
	cfg.diffA2A(&A2AConfig{}, &A2AConfig{}, delta)
	if _, ok := delta["a2a"]; ok {
		t.Fatalf("identical configs should not produce a2a delta: %v", delta)
	}

	cur := &A2AConfig{}
	cur.Disabled = true
	cur.Port = 9100
	cur.Auth.APIKey = "secret"
	cur.Auth.APIKeys = []string{"k1", "k2"}
	cur.MaxTasks = 8
	cur.TaskTimeout = "10m"

	delta = map[string]interface{}{}
	cfg.diffA2A(cur, &A2AConfig{}, delta)
	a2a, ok := delta["a2a"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a2a delta, got %v", delta)
	}
	for _, key := range []string{"disabled", "port", "max_tasks", "task_timeout"} {
		if _, ok := a2a[key]; !ok {
			t.Errorf("a2a delta missing %q: %v", key, a2a)
		}
	}
	auth, ok := a2a["auth"].(map[string]interface{})
	if !ok {
		t.Fatalf("a2a delta missing auth: %v", a2a)
	}
	if auth["api_key"] != "secret" {
		t.Errorf("auth.api_key = %v", auth["api_key"])
	}
	if _, ok := auth["api_keys"]; !ok {
		t.Errorf("auth.api_keys missing: %v", auth)
	}
}

func TestDiffKnight(t *testing.T) {
	cfg := &Config{}

	delta := map[string]interface{}{}
	cfg.diffKnight(&KnightConfig{}, &KnightConfig{}, delta)
	if _, ok := delta["knight"]; ok {
		t.Fatalf("identical configs should not produce knight delta: %v", delta)
	}

	cur := &KnightConfig{}
	cur.Enabled = true
	cur.TrustLevel = "strict"
	cur.DailyTokenBudget = 100000
	cur.IdleDelaySec = 60
	cur.Capabilities = []string{"review"}
	cur.Vendor = "zai"
	cur.Endpoint = "default"
	cur.Model = "glm-4"

	delta = map[string]interface{}{}
	cfg.diffKnight(cur, &KnightConfig{}, delta)
	k, ok := delta["knight"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected knight delta, got %v", delta)
	}
	for _, key := range []string{"enabled", "trust_level", "daily_token_budget", "idle_delay_sec", "capabilities", "vendor", "endpoint", "model"} {
		if _, ok := k[key]; !ok {
			t.Errorf("knight delta missing %q: %v", key, k)
		}
	}
}

func TestDiffSubAgents(t *testing.T) {
	cfg := &Config{}

	delta := map[string]interface{}{}
	cfg.diffSubAgents(&SubAgentConfig{}, &SubAgentConfig{}, delta)
	if _, ok := delta["subagents"]; ok {
		t.Fatalf("identical configs should not produce subagents delta: %v", delta)
	}

	cur := &SubAgentConfig{}
	cur.MaxConcurrent = 4
	cur.Timeout = 5 * time.Minute

	delta = map[string]interface{}{}
	cfg.diffSubAgents(cur, &SubAgentConfig{}, delta)
	s, ok := delta["subagents"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected subagents delta, got %v", delta)
	}
	if s["max_concurrent"] != 4 {
		t.Errorf("max_concurrent = %v", s["max_concurrent"])
	}
	if s["timeout"] != "5m0s" {
		t.Errorf("timeout = %v, want duration string 5m0s", s["timeout"])
	}
}

func TestDiffSwarm(t *testing.T) {
	cfg := &Config{}

	delta := map[string]interface{}{}
	cfg.diffSwarm(&SwarmConfig{}, &SwarmConfig{}, delta)
	if _, ok := delta["swarm"]; ok {
		t.Fatalf("identical configs should not produce swarm delta: %v", delta)
	}

	cur := &SwarmConfig{}
	cur.MaxTeammatesPerTeam = 6
	cur.TeammateTimeout = 90 * time.Second
	cur.InboxSize = 128
	cur.PollInterval = 2 * time.Second

	delta = map[string]interface{}{}
	cfg.diffSwarm(cur, &SwarmConfig{}, delta)
	s, ok := delta["swarm"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected swarm delta, got %v", delta)
	}
	if s["max_teammates_per_team"] != 6 {
		t.Errorf("max_teammates_per_team = %v", s["max_teammates_per_team"])
	}
	if s["teammate_timeout"] != "1m30s" {
		t.Errorf("teammate_timeout = %v", s["teammate_timeout"])
	}
	if s["inbox_size"] != 128 {
		t.Errorf("inbox_size = %v", s["inbox_size"])
	}
	if s["poll_interval"] != "2s" {
		t.Errorf("poll_interval = %v", s["poll_interval"])
	}
}

func TestDiffVendorsAndToolPerms(t *testing.T) {
	cfg := &Config{}

	// Vendor only in current → included; identical vendor → excluded.
	delta := map[string]interface{}{}
	cfg.diffVendors(
		map[string]VendorConfig{"new": {DisplayName: "New"}, "same": {DisplayName: "Same"}},
		map[string]VendorConfig{"same": {DisplayName: "Same"}},
		delta,
	)
	v, ok := delta["vendors"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected vendors delta, got %v", delta)
	}
	if _, ok := v["new"]; !ok {
		t.Error("vendor only in current should be included")
	}
	if _, ok := v["same"]; ok {
		t.Error("identical vendor should be excluded")
	}

	// tool_permissions: value change on globally-known key must be persisted (#524).
	delta = map[string]interface{}{}
	cfg.diffToolPerms(
		map[string]ToolPermission{"run_command": ToolPermDeny},
		map[string]ToolPermission{"run_command": ToolPermAllow},
		delta,
	)
	p, ok := delta["tool_permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_permissions delta, got %v", delta)
	}
	if p["run_command"] != string(ToolPermDeny) {
		t.Errorf("run_command = %v", p["run_command"])
	}
}
