package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateSystemPromptVersion_NoPromptNoVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("vendor: zai\nmodel: glm-5-turbo\n"), 0600)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Should get latest default prompt
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Error("expected default system prompt")
	}
	if cfg.SystemPromptVersion != DefaultSystemPromptVersion {
		t.Errorf("version = %d, want %d", cfg.SystemPromptVersion, DefaultSystemPromptVersion)
	}
	// YAML should now have version stamped
	data, _ := os.ReadFile(cfgPath)
	if !containsString(string(data), "system_prompt_version:") {
		t.Error("expected system_prompt_version in YAML")
	}
}

func TestMigrateSystemPromptVersion_CustomPromptNoVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("vendor: zai\nsystem_prompt: My custom prompt\n"), 0600)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Should KEEP user's custom prompt
	if cfg.SystemPrompt != "My custom prompt" {
		t.Errorf("system prompt = %q, want %q", cfg.SystemPrompt, "My custom prompt")
	}
	if cfg.SystemPromptVersion != DefaultSystemPromptVersion {
		t.Errorf("version = %d, want %d", cfg.SystemPromptVersion, DefaultSystemPromptVersion)
	}
	// YAML should have version stamped but system_prompt kept
	data, _ := os.ReadFile(cfgPath)
	s := string(data)
	if !containsString(s, "system_prompt: My custom prompt") {
		t.Error("expected system_prompt to be preserved in YAML")
	}
	if !containsString(s, "system_prompt_version:") {
		t.Error("expected system_prompt_version in YAML")
	}
}

func TestMigrateSystemPromptVersion_OldVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("vendor: zai\nsystem_prompt_version: 1\n"), 0600)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Should upgrade to latest default
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Error("expected upgraded default system prompt")
	}
	if cfg.SystemPromptVersion != DefaultSystemPromptVersion {
		t.Errorf("version = %d, want %d", cfg.SystemPromptVersion, DefaultSystemPromptVersion)
	}
}

func TestMigrateSystemPromptVersion_CurrentVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(cfgPath, []byte("vendor: zai\nsystem_prompt: Old prompt\nsystem_prompt_version: 999\n"), 0600)

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// Version is high enough → should NOT touch prompt
	if cfg.SystemPrompt != "Old prompt" {
		t.Errorf("system prompt = %q, want %q", cfg.SystemPrompt, "Old prompt")
	}
}
