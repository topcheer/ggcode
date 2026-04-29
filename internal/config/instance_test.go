package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

func TestInstanceDir(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		wantLen   int // expected hash length
	}{
		{"absolute path", "/home/user/projects/myapp", 16},
		{"with trailing slash", "/home/user/projects/myapp/", 16},
		{"relative path gets resolved", "some/relative/path", 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := InstanceDir(tt.workspace)
			if dir == "" {
				t.Fatal("InstanceDir returned empty string")
			}
			// Should end with a 16-char hex string
			base := filepath.Base(dir)
			if len(base) != tt.wantLen {
				t.Errorf("InstanceDir hash length = %d, want %d (dir=%s)", len(base), tt.wantLen, dir)
			}
			// Should be under ~/.ggcode/instances/
			if !filepath.IsAbs(dir) {
				t.Errorf("InstanceDir should return absolute path, got %s", dir)
			}
			home, _ := os.UserHomeDir()
			expectedBase := filepath.Join(home, ".ggcode", "instances")
			if filepath.Dir(dir) != expectedBase {
				t.Errorf("InstanceDir parent = %s, want %s", filepath.Dir(dir), expectedBase)
			}
		})
	}

	// Same workspace should produce same hash
	dir1 := InstanceDir("/home/user/projects/myapp")
	dir2 := InstanceDir("/home/user/projects/myapp")
	if dir1 != dir2 {
		t.Errorf("same workspace should produce same hash: %s != %s", dir1, dir2)
	}

	// Different workspaces should produce different hashes
	dir3 := InstanceDir("/home/user/projects/other")
	if dir1 == dir3 {
		t.Error("different workspaces should produce different hashes")
	}
}

func TestInstanceConfigPath(t *testing.T) {
	path := InstanceConfigPath("/home/user/projects/myapp")
	if path == "" {
		t.Fatal("InstanceConfigPath returned empty string")
	}
	if filepath.Base(path) != "ggcode.yaml" {
		t.Errorf("InstanceConfigPath should end with ggcode.yaml, got %s", path)
	}
}

func TestLoadInstanceConfig_NotExists(t *testing.T) {
	cfg := LoadInstanceConfig("/nonexistent/workspace/path")
	if cfg != nil {
		t.Error("LoadInstanceConfig should return nil for nonexistent workspace")
	}
}

func TestLoadInstanceConfig_Exists(t *testing.T) {
	// Create a temp instance config
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	// Write an instance config
	instCfg := []byte("model: gpt-4o-mini\nvendor: openai\nlanguage: zh-CN\n")
	if err := os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), instCfg, 0644); err != nil {
		t.Fatal(err)
	}

	cfg := LoadInstanceConfig(workspace)
	if cfg == nil {
		t.Fatal("LoadInstanceConfig should return config")
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want %q", cfg.Model, "gpt-4o-mini")
	}
	if cfg.Vendor != "openai" {
		t.Errorf("Vendor = %q, want %q", cfg.Vendor, "openai")
	}
	if cfg.Language != "zh-CN" {
		t.Errorf("Language = %q, want %q", cfg.Language, "zh-CN")
	}
}

func TestHasInstanceConfig(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	if HasInstanceConfig(workspace) {
		t.Error("HasInstanceConfig should be false before creating config")
	}

	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("model: gpt-4o-mini\n"), 0644)

	if !HasInstanceConfig(workspace) {
		t.Error("HasInstanceConfig should be true after creating config")
	}
}

func TestMergeInstance_ScalarFields(t *testing.T) {
	tests := []struct {
		name      string
		global    Config
		instance  Config
		wantModel string
		wantLang  string
	}{
		{
			name:      "instance fills empty global field",
			global:    Config{Model: ""},
			instance:  Config{Model: "gpt-4o-mini"},
			wantModel: "gpt-4o-mini",
		},
		{
			name:      "instance does not override non-empty global",
			global:    Config{Model: "gpt-4o"},
			instance:  Config{Model: "gpt-4o-mini"},
			wantModel: "gpt-4o",
		},
		{
			name:     "instance language fills gap",
			global:   Config{Language: ""},
			instance: Config{Language: "zh-CN"},
			wantLang: "zh-CN",
		},
		{
			name:     "instance language does not override",
			global:   Config{Language: "en"},
			instance: Config{Language: "zh-CN"},
			wantLang: "en",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			MergeInstance(&tt.global, &tt.instance)
			if tt.wantModel != "" && tt.global.Model != tt.wantModel {
				t.Errorf("Model = %q, want %q", tt.global.Model, tt.wantModel)
			}
			if tt.wantLang != "" && tt.global.Language != tt.wantLang {
				t.Errorf("Language = %q, want %q", tt.global.Language, tt.wantLang)
			}
		})
	}
}

func TestMergeInstance_Vendors(t *testing.T) {
	global := &Config{
		Vendors: map[string]VendorConfig{
			"openai": {Endpoints: map[string]EndpointConfig{
				"main": {Protocol: "openai"},
			}},
		},
	}
	instance := &Config{
		Vendors: map[string]VendorConfig{
			"anthropic": {Endpoints: map[string]EndpointConfig{
				"main": {Protocol: "anthropic"},
			}},
		},
	}

	MergeInstance(global, instance)

	// Global vendor should be preserved
	if _, ok := global.Vendors["openai"]; !ok {
		t.Error("global vendor 'openai' should be preserved")
	}
	// Instance vendor should be added (not in global)
	if _, ok := global.Vendors["anthropic"]; !ok {
		t.Error("instance vendor 'anthropic' should be added")
	}
}

func TestMergeInstance_VendorsNoOverride(t *testing.T) {
	global := &Config{
		Vendors: map[string]VendorConfig{
			"openai": {Endpoints: map[string]EndpointConfig{
				"main": {Protocol: "openai", BaseURL: "https://api.openai.com"},
			}},
		},
	}
	instance := &Config{
		Vendors: map[string]VendorConfig{
			"openai": {Endpoints: map[string]EndpointConfig{
				"main": {Protocol: "openai", BaseURL: "https://custom.api.com"},
			}},
		},
	}

	MergeInstance(global, instance)

	// Global vendor should NOT be overridden (same key exists in global)
	if global.Vendors["openai"].Endpoints["main"].BaseURL != "https://api.openai.com" {
		t.Errorf("global vendor base_url should not be overridden, got %s", global.Vendors["openai"].Endpoints["main"].BaseURL)
	}
}

func TestMergeInstance_IMAdapters(t *testing.T) {
	global := &Config{
		IM: IMConfig{
			Adapters: map[string]IMAdapterConfig{
				"slack": {Platform: "slack"},
			},
		},
	}
	instance := &Config{
		IM: IMConfig{
			Adapters: map[string]IMAdapterConfig{
				"feishu": {Platform: "feishu"},
			},
		},
	}

	MergeInstance(global, instance)

	if _, ok := global.IM.Adapters["slack"]; !ok {
		t.Error("global adapter 'slack' should be preserved")
	}
	if _, ok := global.IM.Adapters["feishu"]; !ok {
		t.Error("instance adapter 'feishu' should be added")
	}
}

func TestMergeInstance_Slices(t *testing.T) {
	// MCPServers: instance only used when global is empty
	t.Run("mcp_servers instance fills empty global", func(t *testing.T) {
		global := &Config{}
		instance := &Config{
			MCPServers: []MCPServerConfig{
				{Name: "filesystem", Command: "npx"},
			},
		}
		MergeInstance(global, instance)
		if len(global.MCPServers) != 1 {
			t.Errorf("MCPServers len = %d, want 1", len(global.MCPServers))
		}
	})

	t.Run("mcp_servers instance does not override non-empty global", func(t *testing.T) {
		global := &Config{
			MCPServers: []MCPServerConfig{
				{Name: "github", Command: "npx"},
			},
		}
		instance := &Config{
			MCPServers: []MCPServerConfig{
				{Name: "filesystem", Command: "npx"},
			},
		}
		MergeInstance(global, instance)
		if len(global.MCPServers) != 1 || global.MCPServers[0].Name != "github" {
			t.Errorf("MCPServers should be preserved from global, got %v", global.MCPServers)
		}
	})
}

func TestSaveInstance(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg := &Config{
		Model:    "gpt-4o-mini",
		Vendor:   "openai",
		Language: "zh-CN",
	}

	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance error: %v", err)
	}

	// Verify file exists
	instPath := InstanceConfigPath(workspace)
	if _, err := os.Stat(instPath); err != nil {
		t.Fatalf("instance config file not found: %v", err)
	}

	// Verify content
	loaded := LoadInstanceConfig(workspace)
	if loaded == nil {
		t.Fatal("LoadInstanceConfig returned nil")
	}
	if loaded.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want %q", loaded.Model, "gpt-4o-mini")
	}
	if loaded.Language != "zh-CN" {
		t.Errorf("Language = %q, want %q", loaded.Language, "zh-CN")
	}
}

func TestSaveGlobalNoLeak(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a global config file (minimal)
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	globalContent := "language: en\n"
	os.WriteFile(globalPath, []byte(globalContent), 0644)

	// Load global config
	cfg, _ := Load(globalPath)
	cfg.globalSnap = deepCopyConfig(cfg)
	cfg.instanceFields = map[string]bool{"default_mode": true} // simulate instance fill

	// Simulate instance merge
	cfg.DefaultMode = "auto"

	// Save should not leak instance-only fields
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Read back the saved file and check it doesn't contain leaked fields
	saved, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	savedStr := string(saved)
	if containsYAMLKey(savedStr, "default_mode") {
		t.Errorf("Save() leaked instance field 'default_mode' into global config:\n%s", savedStr)
	}
	// Global fields should be preserved
	if !containsYAMLKey(savedStr, "language") {
		t.Error("Save() lost global field 'language'")
	}
}

func TestSaveGlobalPreservesRuntimeChanges(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a global config file
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	globalContent := "language: en\n"
	os.WriteFile(globalPath, []byte(globalContent), 0644)

	cfg, err := Load(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	// No globalSnap, no instanceFields → normal Save() path
	cfg.Language = "zh-CN"

	// Save should work without error
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Verify language was persisted
	saved, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	savedStr := string(saved)
	// Check that zh-CN appears somewhere in the saved file
	found := false
	for i := 0; i < len(savedStr); i++ {
		if i+5 <= len(savedStr) && savedStr[i:i+5] == "zh-CN" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Save() should contain 'zh-CN' in output (first 200 chars: %q)", savedStr[:min(200, len(savedStr))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestLoadWithInstance(t *testing.T) {
	tmpDir := t.TempDir()

	// Create global config (minimal, no vendor/endpoint)
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	globalContent := "language: en\n"
	os.WriteFile(globalPath, []byte(globalContent), 0644)

	// Create workspace and instance config
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	// Use default_mode which doesn't get a default from Load()
	instContent := "default_mode: auto\n"
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte(instContent), 0644)

	// Load with instance merge
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}

	// Language should be from global
	if cfg.Language != "en" {
		t.Errorf("Language = %q, want %q", cfg.Language, "en")
	}

	// DefaultMode should be filled from instance (global has none — default is empty)
	if cfg.DefaultMode != "auto" {
		t.Errorf("DefaultMode = %q, want %q (instance fills gap)", cfg.DefaultMode, "auto")
	}

	// globalSnap should be set
	if cfg.globalSnap == nil {
		t.Error("globalSnap should be set")
	}
	if cfg.globalSnap.Language != "en" {
		t.Errorf("globalSnap.Language = %q, want %q", cfg.globalSnap.Language, "en")
	}

	// instanceFields should record that default_mode came from instance
	if !cfg.instanceFields["default_mode"] {
		t.Error("instanceFields should contain 'default_mode'")
	}
}

// containsYAMLKey checks if a YAML string contains a top-level key.
func containsYAMLKey(s, key string) bool {
	// Simple check: key: at start of line (possibly with whitespace)
	return len(s) > 0 && (s[:len(key)+1] == key+":" ||
		containsLine(s, key+":"))
}

func containsLine(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			// Check it's at start of line or after newline
			if i == 0 || s[i-1] == '\n' {
				return true
			}
		}
	}
	return false
}

func containsYAMLValue(s, value string) bool {
	return containsLine(s, value) || containsLine(s, "\""+value+"\"")
}

// --- Coverage: InstanceDirPath, HasInstanceConfigAttached, InstanceWorkspace ---

func TestInstanceAccessors_NoInstance(t *testing.T) {
	cfg := &Config{}
	if cfg.InstanceDirPath() != "" {
		t.Error("InstanceDirPath should be empty without instance")
	}
	if cfg.HasInstanceConfigAttached() {
		t.Error("HasInstanceConfigAttached should be false")
	}
	if cfg.InstanceWorkspace() != "" {
		t.Error("InstanceWorkspace should be empty")
	}
}

func TestInstanceAccessors_WithInstance(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg := &Config{}
	cfg.SetInstancePaths(workspace)

	if cfg.InstanceDirPath() == "" {
		t.Error("InstanceDirPath should be set")
	}
	if !cfg.HasInstanceConfigAttached() {
		t.Error("HasInstanceConfigAttached should be true")
	}
	if cfg.InstanceWorkspace() != workspace {
		t.Errorf("InstanceWorkspace = %q, want %q", cfg.InstanceWorkspace(), workspace)
	}
}

// --- Coverage: SaveScoped ---

func TestSaveScoped_Global(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	cfg, _ := Load(globalPath)
	if err := cfg.SaveScoped("global"); err != nil {
		t.Fatalf("SaveScoped(global) error: %v", err)
	}
}

func TestSaveScoped_Instance(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.DefaultMode = "auto"

	if err := cfg.SaveScoped("instance"); err != nil {
		t.Fatalf("SaveScoped(instance) error: %v", err)
	}

	loaded := LoadInstanceConfig(workspace)
	if loaded == nil || loaded.DefaultMode != "auto" {
		t.Errorf("instance config not saved correctly: %+v", loaded)
	}
}

// --- Coverage: InstanceSummary ---

func TestInstanceSummary(t *testing.T) {
	cfg := &Config{}
	if cfg.InstanceSummary() != "no instance config" {
		t.Errorf("InstanceSummary without instance = %q", cfg.InstanceSummary())
	}

	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	cfg.SetInstancePaths(workspace)

	summary := cfg.InstanceSummary()
	if summary == "no instance config" {
		t.Error("InstanceSummary should describe the instance")
	}
}

// --- Coverage: deepCopyConfig ---

func TestDeepCopyConfig_Nil(t *testing.T) {
	result := deepCopyConfig(nil)
	if result != nil {
		t.Error("deepCopyConfig(nil) should return nil")
	}
}

// --- Coverage: MergeInstance complex sub-structs ---

func TestMergeInstance_KnightConfig(t *testing.T) {
	global := &Config{KnightConfig: KnightConfig{Enabled: false}}
	instance := &Config{KnightConfig: KnightConfig{
		Enabled:          true,
		TrustLevel:       "auto",
		DailyTokenBudget: 10_000_000,
		IdleDelaySec:     600,
		Vendor:           "openai",
		Endpoint:         "gpt-4o",
		Model:            "gpt-4o-mini",
		Capabilities:     []string{"skill_creation"},
	}}

	MergeInstance(global, instance)

	if !global.KnightConfig.Enabled {
		t.Error("Knight Enabled should be filled from instance")
	}
	if global.KnightConfig.TrustLevel != "auto" {
		t.Errorf("Knight TrustLevel = %q, want %q", global.KnightConfig.TrustLevel, "auto")
	}
	if global.KnightConfig.DailyTokenBudget != 10_000_000 {
		t.Errorf("Knight DailyTokenBudget = %d, want %d", global.KnightConfig.DailyTokenBudget, 10_000_000)
	}
	if global.KnightConfig.IdleDelaySec != 600 {
		t.Errorf("Knight IdleDelaySec = %d, want %d", global.KnightConfig.IdleDelaySec, 600)
	}
	if global.KnightConfig.Vendor != "openai" {
		t.Errorf("Knight Vendor = %q, want %q", global.KnightConfig.Vendor, "openai")
	}
	if global.KnightConfig.Endpoint != "gpt-4o" {
		t.Errorf("Knight Endpoint = %q, want %q", global.KnightConfig.Endpoint, "gpt-4o")
	}
	if global.KnightConfig.Model != "gpt-4o-mini" {
		t.Errorf("Knight Model = %q, want %q", global.KnightConfig.Model, "gpt-4o-mini")
	}
}

func TestMergeInstance_KnightConfig_GlobalWins(t *testing.T) {
	global := &Config{KnightConfig: KnightConfig{
		TrustLevel:       "staged",
		DailyTokenBudget: 5_000_000,
	}}
	instance := &Config{KnightConfig: KnightConfig{
		TrustLevel:       "auto",
		DailyTokenBudget: 10_000_000,
	}}

	MergeInstance(global, instance)

	if global.KnightConfig.TrustLevel != "staged" {
		t.Errorf("global Knight TrustLevel should win, got %q", global.KnightConfig.TrustLevel)
	}
	if global.KnightConfig.DailyTokenBudget != 5_000_000 {
		t.Errorf("global Knight DailyTokenBudget should win, got %d", global.KnightConfig.DailyTokenBudget)
	}
}

func TestMergeInstance_A2AConfig(t *testing.T) {
	global := &Config{}
	instance := &Config{A2A: A2AConfig{
		Port:         8080,
		Host:         "0.0.0.0",
		APIKey:       "test-key",
		MaxTasks:     10,
		TaskTimeout:  "5m",
		LANDiscovery: true,
	}}

	MergeInstance(global, instance)

	if global.A2A.Port != 8080 {
		t.Errorf("A2A Port = %d, want 8080", global.A2A.Port)
	}
	if global.A2A.Host != "0.0.0.0" {
		t.Errorf("A2A Host = %q, want %q", global.A2A.Host, "0.0.0.0")
	}
	if global.A2A.APIKey != "test-key" {
		t.Errorf("A2A APIKey not set from instance")
	}
	if global.A2A.MaxTasks != 10 {
		t.Errorf("A2A MaxTasks = %d, want 10", global.A2A.MaxTasks)
	}
	if global.A2A.TaskTimeout != "5m" {
		t.Errorf("A2A TaskTimeout = %q, want %q", global.A2A.TaskTimeout, "5m")
	}
	if !global.A2A.LANDiscovery {
		t.Error("A2A LANDiscovery should be true")
	}
}

func TestMergeInstance_A2AConfig_GlobalWins(t *testing.T) {
	global := &Config{A2A: A2AConfig{
		Port:   9090,
		Host:   "127.0.0.1",
		APIKey: "global-key",
	}}
	instance := &Config{A2A: A2AConfig{
		Port:   8080,
		Host:   "0.0.0.0",
		APIKey: "instance-key",
	}}

	MergeInstance(global, instance)

	if global.A2A.Port != 9090 {
		t.Errorf("global A2A Port should win, got %d", global.A2A.Port)
	}
	if global.A2A.Host != "127.0.0.1" {
		t.Errorf("global A2A Host should win, got %q", global.A2A.Host)
	}
	if global.A2A.APIKey != "global-key" {
		t.Errorf("global A2A APIKey should win, got %q", global.A2A.APIKey)
	}
}

func TestMergeInstance_SubAgentConfig(t *testing.T) {
	global := &Config{}
	instance := &Config{SubAgents: SubAgentConfig{
		MaxConcurrent: 5,
		Timeout:       60000000000, // 1 minute
	}}
	MergeInstance(global, instance)
	if global.SubAgents.MaxConcurrent != 5 {
		t.Errorf("SubAgents MaxConcurrent = %d, want 5", global.SubAgents.MaxConcurrent)
	}
}

func TestMergeInstance_SwarmConfig(t *testing.T) {
	global := &Config{}
	instance := &Config{Swarm: SwarmConfig{
		MaxTeammatesPerTeam: 8,
		TeammateTimeout:     30000000000,
		InboxSize:           100,
		PollInterval:        5000000000,
	}}
	MergeInstance(global, instance)
	if global.Swarm.MaxTeammatesPerTeam != 8 {
		t.Errorf("Swarm MaxTeammatesPerTeam = %d, want 8", global.Swarm.MaxTeammatesPerTeam)
	}
}

func TestMergeInstance_HookConfig(t *testing.T) {
	global := &Config{}
	instance := &Config{Hooks: hooks.HookConfig{
		PreToolUse:  []hooks.Hook{{Command: "echo pre"}},
		PostToolUse: []hooks.Hook{{Command: "echo post"}},
	}}
	MergeInstance(global, instance)
	if len(global.Hooks.PreToolUse) != 1 {
		t.Errorf("Hooks PreToolUse len = %d, want 1", len(global.Hooks.PreToolUse))
	}
	if len(global.Hooks.PostToolUse) != 1 {
		t.Errorf("Hooks PostToolUse len = %d, want 1", len(global.Hooks.PostToolUse))
	}
}

func TestMergeInstance_UIConfig(t *testing.T) {
	t.Run("sidebar visible from instance", func(t *testing.T) {
		global := &Config{}
		val := true
		instance := &Config{UI: UIConfig{SidebarVisible: &val}}
		MergeInstance(global, instance)
		if global.UI.SidebarVisible == nil || !*global.UI.SidebarVisible {
			t.Error("SidebarVisible should be true from instance")
		}
	})

	t.Run("sidebar global wins", func(t *testing.T) {
		f := false
		global := &Config{UI: UIConfig{SidebarVisible: &f}}
		val := true
		instance := &Config{UI: UIConfig{SidebarVisible: &val}}
		MergeInstance(global, instance)
		if global.UI.SidebarVisible == nil || *global.UI.SidebarVisible {
			t.Error("global SidebarVisible should win (false)")
		}
	})
}

func TestMergeInstance_IMConfig_AllFields(t *testing.T) {
	global := &Config{}
	reqLocal := true
	instance := &Config{IM: IMConfig{
		Enabled:             true,
		ActiveSessionPolicy: "strict",
		RequireLocalSession: &reqLocal,
		OutputMode:          "quiet",
		Streaming:           IMStreamingConfig{Enabled: true},
		STT:                 IMSTTConfig{Provider: "whisper"},
	}}

	MergeInstance(global, instance)

	if !global.IM.Enabled {
		t.Error("IM Enabled should be true from instance")
	}
	if global.IM.ActiveSessionPolicy != "strict" {
		t.Errorf("IM ActiveSessionPolicy = %q, want %q", global.IM.ActiveSessionPolicy, "strict")
	}
	if global.IM.RequireLocalSession == nil || !*global.IM.RequireLocalSession {
		t.Error("IM RequireLocalSession should be true from instance")
	}
	if global.IM.OutputMode != "quiet" {
		t.Errorf("IM OutputMode = %q, want %q", global.IM.OutputMode, "quiet")
	}
	if !global.IM.Streaming.Enabled {
		t.Error("IM Streaming.Enabled should be true from instance")
	}
	if global.IM.STT.Provider != "whisper" {
		t.Errorf("IM STT.Provider = %q, want %q", global.IM.STT.Provider, "whisper")
	}
}

func TestMergeInstance_IMConfig_GlobalWins(t *testing.T) {
	reqLocal := true
	global := &Config{IM: IMConfig{
		Enabled:             true,
		ActiveSessionPolicy: "lenient",
		OutputMode:          "verbose",
		RequireLocalSession: &reqLocal,
	}}
	reqLocal2 := false
	instance := &Config{IM: IMConfig{
		ActiveSessionPolicy: "strict",
		OutputMode:          "quiet",
		RequireLocalSession: &reqLocal2,
	}}

	MergeInstance(global, instance)

	if global.IM.ActiveSessionPolicy != "lenient" {
		t.Errorf("global IM ActiveSessionPolicy should win, got %q", global.IM.ActiveSessionPolicy)
	}
	if global.IM.OutputMode != "verbose" {
		t.Errorf("global IM OutputMode should win, got %q", global.IM.OutputMode)
	}
	if *global.IM.RequireLocalSession != true {
		t.Error("global IM RequireLocalSession should win")
	}
}

func TestMergeInstance_Impersonation(t *testing.T) {
	t.Run("instance fills empty global", func(t *testing.T) {
		global := &Config{}
		instance := &Config{Impersonation: ImpersonationConfig{Preset: "claude-code"}}
		MergeInstance(global, instance)
		if global.Impersonation.Preset != "claude-code" {
			t.Errorf("Impersonation.Preset = %q, want %q", global.Impersonation.Preset, "claude-code")
		}
	})
	t.Run("global wins", func(t *testing.T) {
		global := &Config{Impersonation: ImpersonationConfig{Preset: "cursor"}}
		instance := &Config{Impersonation: ImpersonationConfig{Preset: "claude-code"}}
		MergeInstance(global, instance)
		if global.Impersonation.Preset != "cursor" {
			t.Errorf("global Impersonation.Preset should win, got %q", global.Impersonation.Preset)
		}
	})
}

func TestMergeInstance_NilInstance(t *testing.T) {
	global := &Config{Language: "en"}
	MergeInstance(global, nil)
	if global.Language != "en" {
		t.Error("MergeInstance with nil should not change global")
	}
}

// --- Coverage: marshalGlobalOnly edge cases ---

func TestMarshalGlobalOnly_NoInstanceFields(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	cfg, _ := Load(globalPath)
	cfg.globalSnap = deepCopyConfig(cfg)
	// No instanceFields → should just serialize normally

	data, err := cfg.marshalGlobalOnly()
	if err != nil {
		t.Fatalf("marshalGlobalOnly error: %v", err)
	}
	if len(data) == 0 {
		t.Error("marshalGlobalOnly should produce output")
	}
}

func TestMarshalGlobalOnly_WithInstanceFields(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	cfg, _ := Load(globalPath)
	cfg.globalSnap = deepCopyConfig(cfg)
	cfg.instanceFields = map[string]bool{"default_mode": true}
	cfg.DefaultMode = "auto"

	data, err := cfg.marshalGlobalOnly()
	if err != nil {
		t.Fatalf("marshalGlobalOnly error: %v", err)
	}
	s := string(data)
	if containsYAMLKey(s, "default_mode") {
		t.Errorf("marshalGlobalOnly should exclude instance fields:\n%s", s)
	}
}

// --- Coverage: LoadWithInstance no instance config ---

func TestLoadWithInstance_NoInstanceConfig(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "nonexistent")
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "en" {
		t.Errorf("Language = %q, want %q", cfg.Language, "en")
	}
	// globalSnap is always set (records original global state)
	if cfg.globalSnap == nil {
		t.Error("globalSnap should always be set by LoadWithInstance")
	}
	// No instance fields should be recorded
	if len(cfg.instanceFields) > 0 {
		t.Errorf("instanceFields should be empty, got %v", cfg.instanceFields)
	}
}

// --- Coverage: LoadWithInstance with legacy a2a.yaml ---

func TestLoadWithInstance_LegacyA2A(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	// Create .ggcode/a2a.yaml in workspace
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"), []byte("api_key: legacy-key\n"), 0644)

	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.A2A.APIKey != "legacy-key" {
		t.Errorf("A2A APIKey = %q, want %q from legacy a2a.yaml", cfg.A2A.APIKey, "legacy-key")
	}
}

// --- Coverage: writeFileAtomic error paths ---

func TestWriteFileAtomic_BadPath(t *testing.T) {
	err := writeFileAtomic("/nonexistent/dir/file.yaml", []byte("test"), 0644)
	if err == nil {
		t.Error("writeFileAtomic should fail for nonexistent directory")
	}
}

// --- Coverage: remaining merge branches ---

func TestMergeInstance_Scalar_MaxIterations(t *testing.T) {
	global := &Config{}
	instance := &Config{MaxIterations: 50}
	MergeInstance(global, instance)
	if global.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", global.MaxIterations)
	}

	// Global wins
	global2 := &Config{MaxIterations: 30}
	MergeInstance(global2, instance)
	if global2.MaxIterations != 30 {
		t.Errorf("global MaxIterations should win, got %d", global2.MaxIterations)
	}
}

func TestMergeInstance_A2AAuth(t *testing.T) {
	global := &Config{}
	instance := &Config{A2A: A2AConfig{Auth: A2AAuthConfig{
		APIKey:  "inst-key",
		APIKeys: []string{"key1", "key2"},
		OAuth2:  &A2AOAuth2Config{ClientID: "cid"},
		OIDC:    &A2AOIDCConfig{IssuerURL: "https://oidc.example.com"},
		MTLS:    &A2AMTLSConfig{CertFile: "cert.pem"},
	}}}

	MergeInstance(global, instance)

	if global.A2A.Auth.APIKey != "inst-key" {
		t.Errorf("A2A Auth APIKey not set from instance")
	}
	if len(global.A2A.Auth.APIKeys) != 2 {
		t.Errorf("A2A Auth APIKeys len = %d, want 2", len(global.A2A.Auth.APIKeys))
	}
	if global.A2A.Auth.OAuth2 == nil || global.A2A.Auth.OAuth2.ClientID != "cid" {
		t.Error("A2A Auth OAuth2 not set from instance")
	}
	if global.A2A.Auth.OIDC == nil || global.A2A.Auth.OIDC.IssuerURL != "https://oidc.example.com" {
		t.Error("A2A Auth OIDC not set from instance")
	}
	if global.A2A.Auth.MTLS == nil || global.A2A.Auth.MTLS.CertFile != "cert.pem" {
		t.Error("A2A Auth MTLS not set from instance")
	}
}

func TestMergeInstance_A2ADisabled(t *testing.T) {
	global := &Config{}
	instance := &Config{A2A: A2AConfig{Disabled: true}}
	MergeInstance(global, instance)
	if !global.A2A.Disabled {
		t.Error("A2A.Disabled should be true from instance")
	}
}

func TestMergeInstance_AllowedDirs(t *testing.T) {
	global := &Config{}
	instance := &Config{AllowedDirs: []string{"/tmp"}}
	MergeInstance(global, instance)
	if len(global.AllowedDirs) != 1 {
		t.Errorf("AllowedDirs len = %d, want 1", len(global.AllowedDirs))
	}

	// Global wins
	global2 := &Config{AllowedDirs: []string{"/home"}}
	MergeInstance(global2, instance)
	if len(global2.AllowedDirs) != 1 || global2.AllowedDirs[0] != "/home" {
		t.Error("global AllowedDirs should win")
	}
}

func TestMergeInstance_ToolPerms(t *testing.T) {
	global := &Config{}
	instance := &Config{ToolPerms: map[string]ToolPermission{
		"read": ToolPermission("allow"),
	}}
	MergeInstance(global, instance)
	if _, ok := global.ToolPerms["read"]; !ok {
		t.Error("ToolPerms 'read' should be added from instance")
	}
}

func TestMergeInstance_Plugins(t *testing.T) {
	global := &Config{}
	instance := &Config{Plugins: []PluginConfigEntry{{Name: "my-plugin"}}}
	MergeInstance(global, instance)
	if len(global.Plugins) != 1 {
		t.Errorf("Plugins len = %d, want 1", len(global.Plugins))
	}
}

// --- Coverage: edge case — instance fields with empty instance ---

func TestMergeInstance_EmptyInstance(t *testing.T) {
	global := &Config{Language: "en"}
	instance := &Config{}
	MergeInstance(global, instance)
	if global.Language != "en" {
		t.Error("empty instance should not modify global")
	}
}

// --- Coverage: SaveGlobalNoLeak end-to-end via LoadWithInstance ---

func TestSaveGlobalNoLeak_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	// Create instance config
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("default_mode: auto\n"), 0644)

	// Load with instance
	cfg, err := LoadWithInstance(globalPath, workspace)
	if err != nil {
		t.Fatal(err)
	}

	// Verify instance filled the gap
	if cfg.DefaultMode != "auto" {
		t.Errorf("DefaultMode = %q, want %q", cfg.DefaultMode, "auto")
	}

	// Save global — should NOT leak default_mode
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	saved, _ := os.ReadFile(globalPath)
	if containsYAMLKey(string(saved), "default_mode") {
		t.Errorf("Save() leaked instance field 'default_mode':\n%s", string(saved))
	}
	if !containsYAMLKey(string(saved), "language") {
		t.Error("Save() lost global field 'language'")
	}

	// Verify instance config still exists and is intact
	inst := LoadInstanceConfig(workspace)
	if inst == nil || inst.DefaultMode != "auto" {
		t.Error("instance config should be unchanged")
	}
}
