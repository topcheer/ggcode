package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStripDefaultsFromYAML_SaveOnlyCustom(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg := DefaultConfig()
	cfg.FilePath = cfgPath
	// Simulate user customizing one vendor + setting language
	cfg.Language = "zh-CN"
	cfg.Vendors["my-custom"] = VendorConfig{
		DisplayName: "My Custom Vendor",
		APIKey:      "${MY_CUSTOM_KEY}",
		Endpoints: map[string]EndpointConfig{
			"prod": {
				Protocol:      "openai",
				BaseURL:       "https://my-custom.example.com/v1",
				DefaultModel:  "my-model",
				SelectedModel: "my-model",
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)

	t.Logf("Saved main config:\n%s", content)

	// Custom vendor should be in vendors.yaml (external file), NOT in main config
	vendorsPath := VendorsPath(tmpDir)
	vendorsData, _ := os.ReadFile(vendorsPath)
	vendorsContent := string(vendorsData)

	if !strings.Contains(vendorsContent, "my-custom") {
		t.Error("vendors.yaml should contain 'my-custom' vendor")
	}
	// Main config should contain zh-CN
	if !strings.Contains(content, "zh-CN") {
		t.Error("saved config should contain 'zh-CN' language")
	}
	// Should NOT contain default vendor names in main config
	for _, name := range []string{"openai", "gemini", "groq", "perplexity", "together",
		"aihubmix", "ark", "dashscope", "getgoapi", "kimi", "minimax",
		"mistral", "moonshot", "novita", "nvidia", "poe", "requesty",
		"vercel", "zai", "deepseek"} {
		pattern := "\n" + name + ":"
		if strings.Contains(content, pattern) || strings.Contains(vendorsContent, pattern) {
			t.Errorf("default vendor %q should be stripped from both files", name)
		}
	}
	// Also verify the main config file is small (no vendor bloat)
	if len(data) > 2000 {
		t.Errorf("main config too large (%d bytes) — vendors probably not migrated to external file", len(data))
	}
}

func TestStripDefaultsFromYAML_SavePreservesModifiedDefault(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg := DefaultConfig()
	cfg.FilePath = cfgPath
	// User modifies an existing default vendor's endpoint
	cfg.Vendors["anthropic"] = VendorConfig{
		DisplayName: "Anthropic",
		APIKey:      "${ANTHROPIC_API_KEY}",
		Endpoints: map[string]EndpointConfig{
			"api": {
				Protocol:      "anthropic",
				BaseURL:       "https://custom-proxy.example.com", // user changed the URL
				DefaultModel:  "claude-3-5-sonnet-latest",
				SelectedModel: "claude-3-5-sonnet-latest",
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)

	t.Logf("Saved main config:\n%s", content)

	// Customized anthropic vendor should be in vendors.yaml (external file)
	vendorsPath := VendorsPath(tmpDir)
	vendorsData, _ := os.ReadFile(vendorsPath)
	vendorsContent := string(vendorsData)

	if !strings.Contains(vendorsContent, "anthropic:") {
		t.Error("vendors.yaml should contain 'anthropic' vendor (it was customized)")
	}
	if !strings.Contains(vendorsContent, "custom-proxy.example.com") {
		t.Error("vendors.yaml should contain the custom base_url")
	}
	// Main config file should be small (no vendor bloat)
	if len(data) > 2000 {
		t.Errorf("main config too large (%d bytes) — vendors not migrated to external file", len(data))
	}
}

func TestStripDefaultsFromYAML_ModelsInlineFormat(t *testing.T) {
	withTestHome(t)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	cfg := DefaultConfig()
	cfg.FilePath = cfgPath
	// Custom vendor with many models (like an aggregator API)
	cfg.Vendors["aggregator"] = VendorConfig{
		DisplayName: "My Aggregator",
		APIKey:      "${AGG_KEY}",
		Endpoints: map[string]EndpointConfig{
			"api": {
				Protocol:     "openai",
				BaseURL:      "https://aggregator.example.com/v1",
				DefaultModel: "gpt-4o",
				Models: []string{
					"gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo",
					"claude-3-5-sonnet-latest", "claude-3-5-haiku-latest",
					"gemini-1.5-pro", "gemini-1.5-flash",
					"deepseek-chat", "deepseek-coder",
					"llama-3.1-70b", "llama-3.1-8b", "mixtral-8x7b",
				},
				Tags: []string{"official", "router"},
			},
		},
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	content := string(data)
	t.Logf("Saved main config:\n%s", content)

	// Vendor data is now in vendors.yaml (external file)
	vendorsPath := VendorsPath(tmpDir)
	vendorsData, _ := os.ReadFile(vendorsPath)
	vendorsContent := string(vendorsData)
	t.Logf("vendors.yaml:\n%s", vendorsContent)

	// Models should be on a single line (flow style), not one-per-line
	if !strings.Contains(vendorsContent, "models: [") {
		t.Error("models should use inline format 'models: [...]' not block style in vendors.yaml")
	}
	// Tags should also be inline
	if !strings.Contains(vendorsContent, "tags: [") {
		t.Error("tags should use inline format 'tags: [...]' not block style in vendors.yaml")
	}

	// Verify the models line contains actual model names
	if !strings.Contains(vendorsContent, "gpt-4o") {
		t.Error("vendors.yaml should contain 'gpt-4o' in models")
	}
	if !strings.Contains(vendorsContent, "llama-3.1-70b") {
		t.Error("vendors.yaml should contain 'llama-3.1-70b' in models")
	}

	// Main config should be compact
	lineCount := strings.Count(content, "\n") + 1
	t.Logf("Main config lines: %d", lineCount)
	if lineCount > 50 {
		t.Errorf("main config too large (%d lines) — vendor data should be in vendors.yaml", lineCount)
	}
}

func TestMigrateToCompactFormat(t *testing.T) {
	withTestHome(t)
	// Create a config file in the OLD verbose format (block-style models + default vendors)
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "ggcode.yaml")

	// Simulate old format: user has language + one custom vendor,
	// but also has ALL 22 default vendors written with block-style models.
	// The vendors use the exact DefaultConfig() content, so they should be stripped.
	//
	// We build the file by serializing DefaultConfig (to get exact defaults)
	// plus adding a custom vendor and changing language.
	cfg := DefaultConfig()
	cfg.Vendors["my-custom"] = VendorConfig{
		DisplayName: "My Custom",
		APIKey:      "${MY_CUSTOM_KEY}",
		Endpoints: map[string]EndpointConfig{
			"prod": {
				Protocol:      "openai",
				BaseURL:       "https://custom.example.com",
				DefaultModel:  "my-model",
				SelectedModel: "my-model",
				Models:        []string{"my-model", "my-model-v2"},
			},
		},
	}
	cfg.Language = "zh-CN"

	// Write it the OLD way: full yaml.Marshal without stripping/compacting
	oldData, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	os.WriteFile(cfgPath, oldData, 0644)
	originalLines := strings.Count(string(oldData), "\n") + 1
	t.Logf("Original: %d lines, %d bytes", originalLines, len(oldData))

	// Trigger migration by loading and saving
	cfg, err = Load(cfgPath)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	cfg.globalSnap = nil
	cfg.instanceFields = nil
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Read back the main config file
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	newContent := string(data)
	newLines := strings.Count(newContent, "\n") + 1

	t.Logf("After: %d lines, %d bytes", newLines, len(data))
	t.Logf("Content:\n%s", newContent)

	// Main config should be much smaller (vendors migrated to vendors.yaml)
	if newLines >= originalLines/2 {
		t.Errorf("main config should be much smaller: before=%d after=%d", originalLines, newLines)
	}

	// Main config should NOT contain default vendors (they're stripped/external now)
	for _, name := range []string{"anthropic", "openai", "gemini", "groq", "deepseek"} {
		if strings.Contains(newContent, "    "+name+":") {
			t.Errorf("default vendor %q should be stripped from main config", name)
		}
	}

	// Main config should NOT contain the custom vendor either (it's in vendors.yaml now)
	if strings.Contains(newContent, "my-custom") {
		t.Error("user's custom vendor should be in vendors.yaml, not main config")
	}

	// Vendors.yaml should contain the user's custom vendor
	vendorsData, _ := os.ReadFile(VendorsPath(tmpDir))
	vendorsContent := string(vendorsData)
	if !strings.Contains(vendorsContent, "my-custom") {
		t.Error("user's custom vendor 'my-custom' should be in vendors.yaml")
	}

	// Models in vendors.yaml should be inline, not block style
	if strings.Contains(vendorsContent, "        - ") {
		t.Error("models should use inline format in vendors.yaml, not block style")
	}
}
