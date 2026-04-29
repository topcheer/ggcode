package config

import (
	"os"
	"path/filepath"
	"testing"
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
