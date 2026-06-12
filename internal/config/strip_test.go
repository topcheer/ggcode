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

	t.Logf("Saved config:\n%s", content)
	t.Logf("Lines: %d, Bytes: %d", len(data)/60+1, len(data))

	// Should contain the custom vendor
	if !strings.Contains(content, "my-custom") {
		t.Error("saved config should contain 'my-custom' vendor")
	}
	// Should contain zh-CN
	if !strings.Contains(content, "zh-CN") {
		t.Error("saved config should contain 'zh-CN' language")
	}
	// Should NOT contain default vendor names that were not customized
	// Check for vendor key pattern "    vendorname:" (YAML map key under vendors:)
	for _, name := range []string{"openai", "gemini", "groq", "perplexity", "together",
		"aihubmix", "ark", "dashscope", "getgoapi", "kimi", "minimax",
		"mistral", "moonshot", "novita", "nvidia", "poe", "requesty",
		"vercel", "zai", "deepseek"} {
		pattern := "    " + name + ":"
		if strings.Contains(content, pattern) {
			t.Errorf("saved config should NOT contain default vendor %q", name)
		}
	}
	// Also verify the file is small (no 22-vendor bloat)
	if len(data) > 2000 {
		t.Errorf("saved config too large (%d bytes) — default vendors probably not stripped", len(data))
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

	t.Logf("Saved config:\n%s", content)

	// Should contain anthropic because it was customized
	if !strings.Contains(content, "anthropic:") {
		t.Error("saved config should contain 'anthropic' vendor (it was customized)")
	}
	if !strings.Contains(content, "custom-proxy.example.com") {
		t.Error("saved config should contain the custom base_url")
	}
	// File should be small (only 1 vendor, not 22)
	if len(data) > 2000 {
		t.Errorf("saved config too large (%d bytes) — default vendors probably not stripped", len(data))
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
	t.Logf("Saved config:\n%s", content)

	// Models should be on a single line (flow style), not one-per-line
	if !strings.Contains(content, "models: [") {
		t.Error("models should use inline format 'models: [...]' not block style")
	}
	// Tags should also be inline
	if !strings.Contains(content, "tags: [") {
		t.Error("tags should use inline format 'tags: [...]' not block style")
	}

	// Verify the models line contains actual model names
	if !strings.Contains(content, "gpt-4o") {
		t.Error("models should contain 'gpt-4o'")
	}
	if !strings.Contains(content, "llama-3.1-70b") {
		t.Error("models should contain 'llama-3.1-70b'")
	}

	// Count lines — should be compact, not 15+ lines for models
	lineCount := strings.Count(content, "\n") + 1
	t.Logf("Total lines: %d", lineCount)
	if lineCount > 50 {
		t.Errorf("config too large (%d lines) — models probably not inlined", lineCount)
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

	// Read back the file
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	newContent := string(data)
	newLines := strings.Count(newContent, "\n") + 1

	t.Logf("After: %d lines, %d bytes", newLines, len(data))
	t.Logf("Content:\n%s", newContent)

	// Should be much smaller (stripped all 22 default vendors)
	if newLines >= originalLines/2 {
		t.Errorf("file should be much smaller: before=%d after=%d", originalLines, newLines)
	}

	// Should NOT contain default vendors that match DefaultConfig exactly
	for _, name := range []string{"anthropic", "openai", "gemini", "groq", "deepseek"} {
		if strings.Contains(newContent, "    "+name+":") {
			t.Errorf("default vendor %q should be stripped (it matches DefaultConfig)", name)
		}
	}

	// Should contain the user's custom vendor
	if !strings.Contains(newContent, "my-custom") {
		t.Error("user's custom vendor 'my-custom' should be preserved")
	}

	// Models should be inline, not block style
	if strings.Contains(newContent, "        - ") {
		t.Error("models should use inline format, not block style with '- '")
	}
}
