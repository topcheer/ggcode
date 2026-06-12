package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
)

// withTestHome redirects HOME to a temp dir to prevent test pollution of ~/.ggcode/.
func withTestHome(t *testing.T) {
	t.Helper()
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
}

func TestInstanceDir(t *testing.T) {
	withTestHome(t)
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
}

func TestInstanceDirNormalizesSymlinks(t *testing.T) {
	withTestHome(t)
	realDir := filepath.Join(t.TempDir(), "real-workspace")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real workspace: %v", err)
	}
	linkDir := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if got, want := InstanceDir(linkDir), InstanceDir(realDir); got != want {
		t.Fatalf("InstanceDir should normalize symlinks: got %s want %s", got, want)
	}

}

func TestInstanceDirDiffersAcrossWorkspaces(t *testing.T) {
	withTestHome(t)
	dir1 := InstanceDir("/home/user/projects/myapp")
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
	withTestHome(t)
	cfg := LoadInstanceConfig("/nonexistent/workspace/path")
	if cfg != nil {
		t.Error("LoadInstanceConfig should return nil for nonexistent workspace")
	}
}

func TestLoadInstanceConfig_Exists(t *testing.T) {
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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

func TestSaveInstanceSecuresPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not reliable on Windows")
	}

	withTestHome(t)
	workspace := filepath.Join(t.TempDir(), "project")
	instDir := InstanceDir(workspace)
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(instPath, []byte("language: en\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(instDir, 0o755); err != nil {
		t.Fatalf("Chmod(dir) error = %v", err)
	}
	if err := os.Chmod(instPath, 0o644); err != nil {
		t.Fatalf("Chmod(path) error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.globalSnap = deepCopyConfig(cfg)
	cfg.Language = "zh-CN"
	cfg.SetInstancePaths(workspace)
	if err := cfg.SaveInstance(workspace); err != nil {
		t.Fatalf("SaveInstance() error = %v", err)
	}

	info, err := os.Stat(instPath)
	if err != nil {
		t.Fatalf("Stat(path) error = %v", err)
	}
	if got := info.Mode().Perm(); got != secureConfigFileMode {
		t.Fatalf("instance config mode = %o, want %o", got, secureConfigFileMode)
	}
	dirInfo, err := os.Stat(instDir)
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != secureConfigDirMode {
		t.Fatalf("instance dir mode = %o, want %o", got, secureConfigDirMode)
	}
}

func TestSaveGlobalNoLeak(t *testing.T) {
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	cfg, _ := Load(globalPath)
	if err := cfg.SaveScoped("global"); err != nil {
		t.Fatalf("SaveScoped(global) error: %v", err)
	}
}

func TestSaveScoped_Instance(t *testing.T) {
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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
	// SetInstancePaths is always called — allows user to switch to instance scope
	// even when no instance config file exists yet.
	if !cfg.HasInstanceConfigAttached() {
		t.Error("HasInstanceConfigAttached should be true (instanceWS set)")
	}
	if cfg.InstanceWorkspace() != workspace {
		t.Errorf("InstanceWorkspace = %q, want %q", cfg.InstanceWorkspace(), workspace)
	}
	// But no instance file exists yet
	if cfg.HasInstanceConfigFile() {
		t.Error("HasInstanceConfigFile should be false (no file created yet)")
	}
}

// --- Coverage: LoadWithInstance with legacy a2a.yaml ---

func TestLoadWithInstance_LegacyA2A(t *testing.T) {
	withTestHome(t)
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
	withTestHome(t)
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
	withTestHome(t)
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

// --- Coverage: MigrateA2AYaml ---

func TestMigrateA2AYaml_NoLegacyFile(t *testing.T) {
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	if MigrateA2AYaml(workspace) {
		t.Error("MigrateA2AYaml should return false when no legacy file exists")
	}
}

func TestMigrateA2AYaml_AlreadyMigrated(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"), []byte("api_key: test\n"), 0644)

	// Create instance config
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("language: en\n"), 0644)

	if MigrateA2AYaml(workspace) {
		t.Error("MigrateA2AYaml should return false when instance config already exists")
	}
}

func TestMigrateA2AYaml_Success(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(filepath.Join(workspace, ".ggcode"), 0755)
	os.WriteFile(filepath.Join(workspace, ".ggcode", "a2a.yaml"),
		[]byte("api_key: migrated-key\nhost: 0.0.0.0\n"), 0644)

	if !MigrateA2AYaml(workspace) {
		t.Fatal("MigrateA2AYaml should return true on successful migration")
	}
	// Verify instance config was created with a2a content
	inst := LoadInstanceConfig(workspace)
	if inst == nil {
		t.Fatal("instance config should exist after migration")
	}
	if inst.A2A.APIKey != "migrated-key" {
		t.Errorf("A2A.APIKey = %q, want %q", inst.A2A.APIKey, "migrated-key")
	}
	if inst.A2A.Host != "0.0.0.0" {
		t.Errorf("A2A.Host = %q, want %q", inst.A2A.Host, "0.0.0.0")
	}

	// Legacy file should still exist (not deleted)
	if _, err := os.Stat(filepath.Join(workspace, ".ggcode", "a2a.yaml")); err != nil {
		t.Error("legacy .ggcode/a2a.yaml should still exist after migration")
	}

	// Second call should return false (already migrated)
	if MigrateA2AYaml(workspace) {
		t.Error("second MigrateA2AYaml call should return false")
	}
}

func TestMigrateA2AYaml_SecuresPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not reliable on Windows")
	}

	withTestHome(t)
	workspace := filepath.Join(t.TempDir(), "project")
	legacyDir := filepath.Join(workspace, ".ggcode")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	legacyPath := filepath.Join(legacyDir, "a2a.yaml")
	if err := os.WriteFile(legacyPath, []byte("api_key: legacy-key\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if !MigrateA2AYaml(workspace) {
		t.Fatal("expected MigrateA2AYaml() to migrate legacy config")
	}

	instDir := InstanceDir(workspace)
	instPath := filepath.Join(instDir, "ggcode.yaml")
	info, err := os.Stat(instPath)
	if err != nil {
		t.Fatalf("Stat(path) error = %v", err)
	}
	if got := info.Mode().Perm(); got != secureConfigFileMode {
		t.Fatalf("instance config mode = %o, want %o", got, secureConfigFileMode)
	}
	dirInfo, err := os.Stat(instDir)
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != secureConfigDirMode {
		t.Fatalf("instance dir mode = %o, want %o", got, secureConfigDirMode)
	}
}

// --- Coverage: patchConfigFile & effectiveFilePath ---

func TestEffectiveFilePath_Global(t *testing.T) {
	cfg := &Config{FilePath: "/home/user/.ggcode/ggcode.yaml"}
	if cfg.effectiveFilePath("global") != "/home/user/.ggcode/ggcode.yaml" {
		t.Error("effectiveFilePath should return FilePath for global scope")
	}
	if cfg.effectiveFilePath("") != "/home/user/.ggcode/ggcode.yaml" {
		t.Error("effectiveFilePath should default to FilePath")
	}
}

func TestEffectiveFilePath_Instance(t *testing.T) {
	cfg := &Config{
		FilePath:     "/home/user/.ggcode/ggcode.yaml",
		instancePath: "/home/user/.ggcode/instances/abc123/ggcode.yaml",
	}
	if cfg.effectiveFilePath("instance") != "/home/user/.ggcode/instances/abc123/ggcode.yaml" {
		t.Error("effectiveFilePath should return instancePath for instance scope")
	}
}

func TestEffectiveFilePath_InstanceFallback(t *testing.T) {
	cfg := &Config{FilePath: "/home/user/.ggcode/ggcode.yaml"}
	// instancePath is empty → fallback to FilePath
	if cfg.effectiveFilePath("instance") != "/home/user/.ggcode/ggcode.yaml" {
		t.Error("effectiveFilePath should fallback to FilePath when instancePath is empty")
	}
}

func TestPatchConfigFile_Global(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	cfg, _ := Load(globalPath)
	err := cfg.patchConfigFile(func(raw map[string]interface{}) {
		raw["language"] = "zh-CN"
	})
	if err != nil {
		t.Fatalf("patchConfigFile error: %v", err)
	}

	// Verify file was patched
	data, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(data), "zh-CN") {
		t.Errorf("file should contain zh-CN:\n%s", string(data))
	}
}

func TestPatchConfigFile_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	// Create instance config
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("default_mode: auto\n"), 0644)

	err := cfg.patchConfigFile(func(raw map[string]interface{}) {
		raw["default_mode"] = "supervised"
	})
	if err != nil {
		t.Fatalf("patchConfigFile error: %v", err)
	}

	// Verify instance file was patched (not global)
	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "supervised") {
		t.Errorf("instance file should contain supervised:\n%s", string(instData))
	}
	// Global file should be unchanged
	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "supervised") {
		t.Error("global file should NOT contain supervised")
	}
}

func TestPatchConfigFile_NewFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	fp := filepath.Join(tmpDir, "newdir", "ggcode.yaml")

	cfg := &Config{FilePath: fp}
	err := cfg.patchConfigFile(func(raw map[string]interface{}) {
		raw["language"] = "en"
	})
	if err != nil {
		t.Fatalf("patchConfigFile error for new file: %v", err)
	}

	data, _ := os.ReadFile(fp)
	if !strings.Contains(string(data), "language") {
		t.Errorf("new file should contain language:\n%s", string(data))
	}
}

func TestSaveLanguagePreference_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	// Create instance config
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("default_mode: auto\n"), 0644)

	if err := cfg.SaveLanguagePreference("zh-TW"); err != nil {
		t.Fatalf("SaveLanguagePreference error: %v", err)
	}

	// Language should be in instance config
	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "zh-TW") {
		t.Errorf("instance config should contain zh-TW:\n%s", string(instData))
	}
	// Global should be unchanged
	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "zh-TW") {
		t.Error("global config should NOT contain zh-TW")
	}
}

func TestSaveDefaultModePreference_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	if err := cfg.SaveDefaultModePreference("auto"); err != nil {
		t.Fatalf("SaveDefaultModePreference error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "auto") {
		t.Errorf("instance config should contain auto mode:\n%s", string(instData))
	}
}

func TestSaveDefaultModePreference_Invalid(t *testing.T) {
	withTestHome(t)
	cfg := &Config{FilePath: "/tmp/test.yaml"}
	err := cfg.SaveDefaultModePreference("invalid-mode")
	if err == nil {
		t.Error("SaveDefaultModePreference should reject invalid mode")
	}
}

// --- Instance keys.env isolation tests ---

func TestMigrateInstanceKeys_DoesNotOverwriteGlobal(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()

	// Set up global keys.env with an existing key
	globalKeysEnv := filepath.Join(tmpDir, "keys.env")
	os.WriteFile(globalKeysEnv, []byte("export GGCODE_OPENAI_API_KEY='sk-global-key'\n"), 0600)

	// Override keys.env path for testing
	keysEnvPathOverride = globalKeysEnv
	defer func() { keysEnvPathOverride = "" }()

	// Create instance config with a different key for the same vendor
	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	instPath := filepath.Join(instDir, "ggcode.yaml")
	os.WriteFile(instPath, []byte("vendors:\n  openai:\n    api_key: sk-instance-key\n    endpoints:\n      cn:\n        base_url: https://api.example.com\n"), 0644)

	// Run instance migration
	hash := filepath.Base(instDir)
	findings, err := MigrateInstancePlaintextAPIKeys(instPath, hash)
	if err != nil {
		t.Fatalf("MigrateInstancePlaintextAPIKeys error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected to find plaintext API key in instance config")
	}

	// Verify global keys.env was NOT changed
	globalData, _ := os.ReadFile(globalKeysEnv)
	if strings.Contains(string(globalData), "sk-instance-key") {
		t.Errorf("global keys.env should NOT contain instance key:\n%s", string(globalData))
	}
	if !strings.Contains(string(globalData), "sk-global-key") {
		t.Errorf("global keys.env should still contain global key:\n%s", string(globalData))
	}

	// Verify instance keys.env was created with prefixed var name
	instKeysEnv := filepath.Join(instDir, "keys.env")
	instData, err := os.ReadFile(instKeysEnv)
	if err != nil {
		t.Fatalf("instance keys.env should exist: %v", err)
	}
	if !strings.Contains(string(instData), "GGCODE_I_"+hash+"_") {
		t.Errorf("instance keys.env should use prefixed env var:\n%s", string(instData))
	}
	if strings.Contains(string(instData), "sk-global-key") {
		t.Errorf("instance keys.env should NOT contain global key:\n%s", string(instData))
	}

	// Verify instance YAML now uses ${PREFIXED_VAR} reference
	instYAML, _ := os.ReadFile(instPath)
	if !strings.Contains(string(instYAML), "${GGCODE_I_"+hash+"_") {
		t.Errorf("instance YAML should use prefixed env var reference:\n%s", string(instYAML))
	}
}

func TestLoadInstanceKeysEnv(t *testing.T) {
	tmpDir := t.TempDir()

	// Create instance keys.env with prefixed vars
	instDir := filepath.Join(tmpDir, "inst")
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "keys.env"),
		[]byte("export GGCODE_I_abc123_OPENAI_API_KEY='sk-inst-key'\n"), 0600)

	// Clean env
	os.Unsetenv("GGCODE_I_abc123_OPENAI_API_KEY")

	// Load instance keys
	if err := LoadInstanceKeysEnv(instDir); err != nil {
		t.Fatalf("LoadInstanceKeysEnv error: %v", err)
	}

	val, ok := os.LookupEnv("GGCODE_I_abc123_OPENAI_API_KEY")
	if !ok || val != "sk-inst-key" {
		t.Errorf("env var not set correctly: got %q, want %q", val, "sk-inst-key")
	}
	os.Unsetenv("GGCODE_I_abc123_OPENAI_API_KEY")
}

func TestLoadInstanceKeysEnv_EmptyDir(t *testing.T) {
	if err := LoadInstanceKeysEnv(""); err != nil {
		t.Errorf("LoadInstanceKeysEnv('') should not error: %v", err)
	}
}

func TestLoadInstanceKeysEnv_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := LoadInstanceKeysEnv(tmpDir); err != nil {
		t.Errorf("LoadInstanceKeysEnv with no keys.env should not error: %v", err)
	}
}

// ============================================================
// COMPREHENSIVE INSTANCE CONFIG SAFETY TESTS
// These tests verify that instance-level config operations
// NEVER corrupt, leak into, or overwrite global config.
// ============================================================

// --- SaveImpersonation: instance scope ---

func TestSaveImpersonation_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	err := cfg.SaveImpersonation(ImpersonationConfig{
		Preset: "claude-code",
	})
	if err != nil {
		t.Fatalf("SaveImpersonation error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "claude-code") {
		t.Errorf("instance config should contain impersonation preset:\n%s", string(instData))
	}

	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "claude-code") {
		t.Errorf("global config should NOT contain instance impersonation:\n%s", string(globalData))
	}
}

func TestSaveImpersonation_ClearInstance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("impersonation:\n  preset: claude-code\n"), 0644)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	// Clear impersonation
	err := cfg.SaveImpersonation(ImpersonationConfig{})
	if err != nil {
		t.Fatalf("SaveImpersonation clear error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if strings.Contains(string(instData), "impersonation") {
		t.Errorf("instance config should not have impersonation after clear:\n%s", string(instData))
	}
}

// --- SaveSidebarPreference: instance scope ---

func TestSaveSidebarPreference_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	if err := cfg.SaveSidebarPreference(false); err != nil {
		t.Fatalf("SaveSidebarPreference error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "sidebar_visible") {
		t.Errorf("instance config should contain sidebar_visible:\n%s", string(instData))
	}
}

// --- patchConfigFile: instance key migration ---

func TestPatchConfigFile_InstanceKeyMigration(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalKeysEnv := filepath.Join(tmpDir, "keys.env")
	os.WriteFile(globalKeysEnv, []byte("export GGCODE_OPENAI_API_KEY='sk-global-key'\n"), 0600)
	keysEnvPathOverride = globalKeysEnv
	defer func() { keysEnvPathOverride = "" }()

	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	// Patch with a vendor API key — should trigger instance migration
	err := cfg.patchConfigFile(func(raw map[string]interface{}) {
		raw["vendors"] = map[string]interface{}{
			"openai": map[string]interface{}{
				"api_key": "sk-instance-key-from-patch",
				"endpoints": map[string]interface{}{
					"cn": map[string]interface{}{
						"base_url": "https://api.example.com",
					},
				},
			},
		}
	})
	if err != nil {
		t.Fatalf("patchConfigFile error: %v", err)
	}

	// Global keys.env must be unchanged
	globalData, _ := os.ReadFile(globalKeysEnv)
	if strings.Contains(string(globalData), "sk-instance-key-from-patch") {
		t.Errorf("global keys.env MUST NOT contain instance key:\n%s", string(globalData))
	}
	if !strings.Contains(string(globalData), "sk-global-key") {
		t.Errorf("global keys.env lost its original key:\n%s", string(globalData))
	}

	// Instance keys.env must exist with prefixed key
	instKeysEnv := filepath.Join(instDir, "keys.env")
	instKeysData, err := os.ReadFile(instKeysEnv)
	if err != nil {
		t.Fatalf("instance keys.env should exist: %v", err)
	}
	hash := filepath.Base(instDir)
	if !strings.Contains(string(instKeysData), "GGCODE_I_"+hash+"_") {
		t.Errorf("instance keys.env should use prefixed env var:\n%s", string(instKeysData))
	}
}

// --- AddIMAdapter: instance scope with token ---

func TestAddIMAdapter_Instance_TokenIsolation(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalKeysEnv := filepath.Join(tmpDir, "keys.env")
	os.WriteFile(globalKeysEnv, []byte("export GGCODE_IM_mybot_bot_token='global-bot-token'\n"), 0600)
	keysEnvPathOverride = globalKeysEnv
	defer func() { keysEnvPathOverride = "" }()

	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\nim:\n  enabled: true\n  adapters: {}\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)

	cfg, _ := Load(globalPath)
	cfg.SetInstancePaths(workspace)
	cfg.saveScope = "instance"

	err := cfg.AddIMAdapter("mybot", IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra: map[string]interface{}{
			"bot_token": "123456:instance-bot-token",
		},
	})
	if err != nil {
		t.Fatalf("AddIMAdapter error: %v", err)
	}

	// Verify instance config has the adapter
	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "mybot") {
		t.Errorf("instance config should contain adapter 'mybot':\n%s", string(instData))
	}

	// Global keys.env must still have the original token
	globalData, _ := os.ReadFile(globalKeysEnv)
	if !strings.Contains(string(globalData), "global-bot-token") {
		t.Errorf("global keys.env lost original token:\n%s", string(globalData))
	}
	if strings.Contains(string(globalData), "instance-bot-token") {
		t.Errorf("global keys.env MUST NOT contain instance token:\n%s", string(globalData))
	}

	// Global config must not have the adapter
	globalCfgData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalCfgData), "mybot") {
		t.Errorf("global config MUST NOT contain instance adapter:\n%s", string(globalCfgData))
	}
}

// --- SetIMAdapterExtra: instance scope ---

func TestSetIMAdapterExtra_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\nim:\n  enabled: true\n  adapters: {}\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("im:\n  enabled: true\n  adapters:\n    mybot:\n      platform: telegram\n      enabled: true\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	cfg.saveScope = "instance"

	err := cfg.SetIMAdapterExtra("mybot", "bot_token", "new-instance-token")
	if err != nil {
		t.Fatalf("SetIMAdapterExtra error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "bot_token") {
		t.Errorf("instance config should contain bot_token:\n%s", string(instData))
	}

	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "mybot") || strings.Contains(string(globalData), "bot_token") {
		t.Errorf("global config MUST NOT be affected:\n%s", string(globalData))
	}
}

// --- RemoveIMAdapter: instance scope ---

func TestRemoveIMAdapter_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\nim:\n  enabled: true\n  adapters:\n    globalbot:\n      platform: slack\n      enabled: true\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("im:\n  enabled: true\n  adapters:\n    instbot:\n      platform: telegram\n      enabled: true\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	cfg.saveScope = "instance"

	err := cfg.RemoveIMAdapter("instbot")
	if err != nil {
		t.Fatalf("RemoveIMAdapter error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if strings.Contains(string(instData), "instbot") {
		t.Errorf("instance config should not contain removed adapter:\n%s", string(instData))
	}

	globalData, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(globalData), "globalbot") {
		t.Errorf("global config should still have globalbot:\n%s", string(globalData))
	}
}

// --- SetIMAdapterEnabled: instance scope ---

func TestSetIMAdapterEnabled_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\nim:\n  enabled: true\n  adapters: {}\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("im:\n  enabled: true\n  adapters:\n    mybot:\n      platform: telegram\n      enabled: true\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	cfg.saveScope = "instance"

	err := cfg.SetIMAdapterEnabled("mybot", false)
	if err != nil {
		t.Fatalf("SetIMAdapterEnabled error: %v", err)
	}

	reloaded := LoadInstanceConfig(workspace)
	if reloaded == nil {
		t.Fatal("instance config should exist")
	}
	adapter, ok := reloaded.IM.Adapters["mybot"]
	if !ok {
		t.Fatal("adapter mybot should exist")
	}
	if adapter.Enabled != false {
		t.Errorf("adapter should be disabled, got enabled=%v", adapter.Enabled)
	}
}

// --- AddIMTarget: instance scope ---

func TestAddIMTarget_Instance(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\nim:\n  enabled: true\n  adapters: {}\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("im:\n  enabled: true\n  adapters:\n    mybot:\n      platform: telegram\n      enabled: true\n      targets: []\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	cfg.saveScope = "instance"

	err := cfg.AddIMTarget("mybot", IMTargetConfig{
		ID:      "chat-123",
		Label:   "Test Chat",
		Channel: "-10012345",
	})
	if err != nil {
		t.Fatalf("AddIMTarget error: %v", err)
	}

	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "chat-123") {
		t.Errorf("instance config should contain target:\n%s", string(instData))
	}
}

// ============================================================
// DISASTER SCENARIO TESTS
// Verify that no combination of operations corrupts config.
// ============================================================

// Disaster 1: Global has openai key, instance saves different openai key,
// then global saves again — global key must survive.

func TestDisaster_InstanceThenGlobalSave_KeySurvival(t *testing.T) {
	withTestHome(t)
	// LoadWithInstance merges instance config, then Save() must not leak
	// instance fields into the global file.
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("default_mode: auto\nmax_iterations: 42\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	if cfg.DefaultMode != "auto" {
		t.Fatalf("DefaultMode should be auto from instance, got %q", cfg.DefaultMode)
	}

	// Modify a global-only field
	cfg.Language = "zh-TW"

	// Save to global — instance fields must NOT leak
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	globalData, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(globalData), "zh-TW") {
		t.Errorf("global should have zh-TW:\n%s", string(globalData))
	}
	if strings.Contains(string(globalData), "auto") || strings.Contains(string(globalData), "max_iterations") || strings.Contains(string(globalData), "42") {
		t.Errorf("DISASTER: instance fields leaked into global!\n%s", string(globalData))
	}
}

func TestDisaster_TwoWorkspaces_SameVendor_Isolation(t *testing.T) {
	withTestHome(t)
	// Two workspaces with different instance configs — verify no cross-contamination
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	ws1 := filepath.Join(tmpDir, "ws1")
	ws2 := filepath.Join(tmpDir, "ws2")
	os.MkdirAll(ws1, 0755)
	os.MkdirAll(ws2, 0755)

	// Instance 1: default_mode = auto
	inst1Dir := InstanceDir(ws1)
	os.MkdirAll(inst1Dir, 0755)
	os.WriteFile(filepath.Join(inst1Dir, "ggcode.yaml"), []byte("default_mode: auto\n"), 0644)

	// Instance 2: default_mode = plan
	inst2Dir := InstanceDir(ws2)
	os.MkdirAll(inst2Dir, 0755)
	os.WriteFile(filepath.Join(inst2Dir, "ggcode.yaml"), []byte("default_mode: plan\n"), 0644)

	cfg1, _ := LoadWithInstance(globalPath, ws1)
	cfg2, _ := LoadWithInstance(globalPath, ws2)

	if cfg1.DefaultMode != "auto" {
		t.Errorf("ws1 DefaultMode = %q, want auto", cfg1.DefaultMode)
	}
	if cfg2.DefaultMode != "plan" {
		t.Errorf("ws2 DefaultMode = %q, want plan", cfg2.DefaultMode)
	}

	// Verify global config untouched by both loads
	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "auto") || strings.Contains(string(globalData), "plan") {
		t.Errorf("DISASTER: global config contains instance modes!\n%s", string(globalData))
	}

	// Verify instance 1 config
	inst1Data, _ := os.ReadFile(filepath.Join(inst1Dir, "ggcode.yaml"))
	if strings.Contains(string(inst1Data), "plan") {
		t.Errorf("DISASTER: instance1 config contains ws2 mode 'plan'!\n%s", string(inst1Data))
	}

	// Verify instance 2 config
	inst2Data, _ := os.ReadFile(filepath.Join(inst2Dir, "ggcode.yaml"))
	if strings.Contains(string(inst2Data), "auto") {
		t.Errorf("DISASTER: instance2 config contains ws1 mode 'auto'!\n%s", string(inst2Data))
	}
}

func TestDisaster_RapidScopeToggle_NoCorruption(t *testing.T) {
	withTestHome(t)
	// LoadWithInstance records instanceFields. Then Save() excludes them.
	// This tests the core guarantee: instance fields never appear in global file.
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("default_mode: auto\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)
	if cfg.DefaultMode != "auto" {
		t.Fatalf("DefaultMode = %q, want auto", cfg.DefaultMode)
	}
	if len(cfg.instanceFields) == 0 {
		t.Fatal("instanceFields should be populated")
	}

	// Modify global field and save
	cfg.Language = "zh-CN"
	cfg.saveScope = "global"
	if err := cfg.SaveScoped("global"); err != nil {
		t.Fatalf("SaveScoped(global) error: %v", err)
	}

	// Global: has zh-CN, does NOT have "auto"
	globalData, _ := os.ReadFile(globalPath)
	if !strings.Contains(string(globalData), "zh-CN") {
		t.Errorf("global should have zh-CN:\n%s", string(globalData))
	}
	if strings.Contains(string(globalData), "auto") {
		t.Errorf("DISASTER: global contains instance 'auto'!\n%s", string(globalData))
	}

	// Instance: still has auto
	instData, _ := os.ReadFile(filepath.Join(instDir, "ggcode.yaml"))
	if !strings.Contains(string(instData), "auto") {
		t.Errorf("instance should have 'auto':\n%s", string(instData))
	}
}

func TestSave_LeakPrevention_MultipleInstanceFields(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"),
		[]byte("default_mode: auto\nmax_iterations: 50\n"), 0644)

	cfg, _ := LoadWithInstance(globalPath, workspace)

	// Verify instance fields were merged
	if cfg.DefaultMode != "auto" {
		t.Errorf("DefaultMode = %q, want auto", cfg.DefaultMode)
	}
	if cfg.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", cfg.MaxIterations)
	}
	if len(cfg.instanceFields) == 0 {
		t.Fatal("instanceFields should be populated")
	}

	// Now save global — instance fields must NOT leak
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	globalData, _ := os.ReadFile(globalPath)
	if strings.Contains(string(globalData), "auto") {
		t.Errorf("DISASTER: instance default_mode 'auto' leaked into global!\n%s", string(globalData))
	}
	if strings.Contains(string(globalData), "max_iterations") {
		t.Errorf("DISASTER: instance max_iterations leaked into global!\n%s", string(globalData))
	}
	if !strings.Contains(string(globalData), "language: en") {
		t.Errorf("global lost its own field 'language: en':\n%s", string(globalData))
	}
}
func TestMigrateInstanceKeys_IMAdapterToken(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalKeysEnv := filepath.Join(tmpDir, "keys.env")
	os.WriteFile(globalKeysEnv, []byte("export GGCODE_IM_mybot_bot_token='global-tg-token'\n"), 0600)
	keysEnvPathOverride = globalKeysEnv
	defer func() { keysEnvPathOverride = "" }()

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	instPath := filepath.Join(instDir, "ggcode.yaml")
	os.WriteFile(instPath, []byte("im:\n  adapters:\n    mybot:\n      platform: telegram\n      extra:\n        bot_token: 'instance-tg-token'\n"), 0644)

	hash := filepath.Base(instDir)
	findings, err := MigrateInstancePlaintextAPIKeys(instPath, hash)
	if err != nil {
		t.Fatalf("MigrateInstancePlaintextAPIKeys error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("should find bot_token as plaintext key")
	}

	// Global keys.env must be untouched
	globalData, _ := os.ReadFile(globalKeysEnv)
	if !strings.Contains(string(globalData), "global-tg-token") {
		t.Errorf("global keys.env lost its token:\n%s", string(globalData))
	}
	if strings.Contains(string(globalData), "instance-tg-token") {
		t.Errorf("DISASTER: instance IM token leaked to global keys.env!\n%s", string(globalData))
	}

	// Instance keys.env must have prefixed token
	instKeysData, _ := os.ReadFile(filepath.Join(instDir, "keys.env"))
	prefix := "GGCODE_I_" + hash + "_"
	if !strings.Contains(string(instKeysData), prefix) {
		t.Errorf("instance keys.env should use prefix %s:\n%s", prefix, string(instKeysData))
	}
	if !strings.Contains(string(instKeysData), "instance-tg-token") {
		t.Errorf("instance keys.env should contain instance token:\n%s", string(instKeysData))
	}
}

// --- Verify IM adapter extra merge doesn't lose global extra ---

func TestMergeInstance_IMAdapterExtraMerge(t *testing.T) {
	// When global and instance have same adapter name, global wins (adapter-level isolation).
	// Instance adapter with a NEW name gets added.
	global := &Config{IM: IMConfig{
		Enabled: true,
		Adapters: map[string]IMAdapterConfig{
			"globalbot": {
				Platform: "telegram",
				Extra:    map[string]interface{}{"bot_token": "global-token"},
			},
		},
	}}
	instance := &Config{IM: IMConfig{
		Adapters: map[string]IMAdapterConfig{
			"globalbot": {
				// Same name as global — global wins, this is ignored
				Extra: map[string]interface{}{"bot_token": "instance-token"},
			},
			"instancebot": {
				// New adapter — gets added
				Platform: "slack",
				Extra:    map[string]interface{}{"bot_token": "instance-slack-token"},
			},
		},
	}}

	MergeInstance(global, instance)

	// Global adapter unchanged
	globalBot := global.IM.Adapters["globalbot"]
	if globalBot.Extra["bot_token"] != "global-token" {
		t.Errorf("global adapter should keep its token, got %v", globalBot.Extra["bot_token"])
	}

	// Instance adapter added
	instBot, ok := global.IM.Adapters["instancebot"]
	if !ok {
		t.Fatal("instance adapter 'instancebot' should be added")
	}
	if instBot.Extra["bot_token"] != "instance-slack-token" {
		t.Errorf("instance adapter should have its token, got %v", instBot.Extra["bot_token"])
	}
}

// --- MCP server instance migration ---

func TestMigrateInstanceKeys_MCPServerEnv(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalKeysEnv := filepath.Join(tmpDir, "keys.env")
	os.WriteFile(globalKeysEnv, []byte("export GGCODE_MCP_myserver_api_key='global-mcp-key'\n"), 0600)
	keysEnvPathOverride = globalKeysEnv
	defer func() { keysEnvPathOverride = "" }()

	workspace := filepath.Join(tmpDir, "project")
	os.MkdirAll(workspace, 0755)
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	instPath := filepath.Join(instDir, "ggcode.yaml")
	os.WriteFile(instPath, []byte("mcp_servers:\n  - name: myserver\n    env:\n        api_key: instance-mcp-key\n"), 0644)

	hash := filepath.Base(instDir)
	findings, err := MigrateInstancePlaintextAPIKeys(instPath, hash)
	if err != nil {
		t.Fatalf("MigrateInstancePlaintextAPIKeys error: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("should find MCP env as plaintext key")
	}

	// Global keys.env untouched
	globalData, _ := os.ReadFile(globalKeysEnv)
	if strings.Contains(string(globalData), "instance-mcp-key") {
		t.Errorf("DISASTER: instance MCP key leaked to global:\n%s", string(globalData))
	}

	// Instance keys.env has prefixed MCP key
	instKeysData, _ := os.ReadFile(filepath.Join(instDir, "keys.env"))
	if !strings.Contains(string(instKeysData), "GGCODE_I_"+hash+"_") {
		t.Errorf("instance keys.env should use prefix:\n%s", string(instKeysData))
	}
}

func TestHasInstanceConfigFile(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	globalPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(globalPath, []byte("language: en\n"), 0644)

	workspace := filepath.Join(tmpDir, "project")

	// No instance at all — false
	cfg, _ := Load(globalPath)
	if cfg.HasInstanceConfigFile() {
		t.Error("should be false without LoadWithInstance")
	}

	// LoadWithInstance but no file — false
	cfg, _ = LoadWithInstance(globalPath, workspace)
	if cfg.HasInstanceConfigFile() {
		t.Error("should be false when instance file doesn't exist yet")
	}

	// Create instance file — true
	instDir := InstanceDir(workspace)
	os.MkdirAll(instDir, 0755)
	os.WriteFile(filepath.Join(instDir, "ggcode.yaml"), []byte("language: zh\n"), 0644)
	cfg, _ = LoadWithInstance(globalPath, workspace)
	if !cfg.HasInstanceConfigFile() {
		t.Error("should be true when instance file exists")
	}
}

// --- SetIMAdapterEnabled: global scope ---

func TestSetIMAdapterEnabled_Global(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("language: en\nim:\n  enabled: true\n  adapters:\n    mybot:\n      platform: telegram\n      enabled: true\n"), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Disable
	if err := cfg.SetIMAdapterEnabled("mybot", false); err != nil {
		t.Fatalf("SetIMAdapterEnabled(false): %v", err)
	}

	// Reload and verify persisted
	reloaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	adapter, ok := reloaded.IM.Adapters["mybot"]
	if !ok {
		t.Fatal("adapter mybot should exist after reload")
	}
	if adapter.Enabled != false {
		t.Errorf("adapter should be disabled, got enabled=%v", adapter.Enabled)
	}

	// Re-enable
	if err := reloaded.SetIMAdapterEnabled("mybot", true); err != nil {
		t.Fatalf("SetIMAdapterEnabled(true): %v", err)
	}

	reloaded2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Reload2: %v", err)
	}
	adapter2, ok := reloaded2.IM.Adapters["mybot"]
	if !ok {
		t.Fatal("adapter mybot should exist after reload2")
	}
	if adapter2.Enabled != true {
		t.Errorf("adapter should be enabled, got enabled=%v", adapter2.Enabled)
	}
}

func TestSetIMAdapterEnabled_NotFound(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("language: en\nim:\n  adapters: {}\n"), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = cfg.SetIMAdapterEnabled("nonexistent", false)
	if err == nil {
		t.Fatal("expected error for nonexistent adapter")
	}
}

func TestSetIMAdapterEnabled_NilConfig(t *testing.T) {
	withTestHome(t)
	var cfg *Config
	err := cfg.SetIMAdapterEnabled("mybot", false)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestSetIMAdapterEnabled_NilAdapters(t *testing.T) {
	withTestHome(t)
	cfg := &Config{}
	err := cfg.SetIMAdapterEnabled("mybot", false)
	if err == nil {
		t.Fatal("expected error for nil adapters map")
	}
}

// TestSetIMAdapterEnabled_FullCycle tests the complete cycle:
// disable → persist → reload → verify disabled → enable → persist → reload → verify enabled
func TestSetIMAdapterEnabled_FullCycle(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("language: en\nim:\n  enabled: true\n  adapters:\n    qq1:\n      platform: qq\n      enabled: true\n    tg1:\n      platform: telegram\n      enabled: true\n"), 0644)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Disable qq1
	if err := cfg.SetIMAdapterEnabled("qq1", false); err != nil {
		t.Fatalf("SetIMAdapterEnabled qq1 false: %v", err)
	}

	// Reload — qq1 should be disabled, tg1 enabled
	cfg2, _ := Load(cfgPath)
	if cfg2.IM.Adapters["qq1"].Enabled != false {
		t.Error("qq1 should be disabled after reload")
	}
	if cfg2.IM.Adapters["tg1"].Enabled != true {
		t.Error("tg1 should remain enabled")
	}

	// Disable tg1 too
	if err := cfg2.SetIMAdapterEnabled("tg1", false); err != nil {
		t.Fatalf("SetIMAdapterEnabled tg1 false: %v", err)
	}

	// Reload — both disabled
	cfg3, _ := Load(cfgPath)
	if cfg3.IM.Adapters["qq1"].Enabled != false {
		t.Error("qq1 should still be disabled")
	}
	if cfg3.IM.Adapters["tg1"].Enabled != false {
		t.Error("tg1 should be disabled")
	}

	// Re-enable qq1
	if err := cfg3.SetIMAdapterEnabled("qq1", true); err != nil {
		t.Fatalf("SetIMAdapterEnabled qq1 true: %v", err)
	}

	// Reload — qq1 enabled, tg1 disabled
	cfg4, _ := Load(cfgPath)
	if cfg4.IM.Adapters["qq1"].Enabled != true {
		t.Error("qq1 should be re-enabled")
	}
	if cfg4.IM.Adapters["tg1"].Enabled != false {
		t.Error("tg1 should remain disabled")
	}
}
