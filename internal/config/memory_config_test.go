package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad_MemorySectionParses covers the memory v2 yaml section: the
// prompt_inject pointer/full switch and the curator enable/model override.
func TestLoad_MemorySectionParses(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	yaml := `memory:
  prompt_inject: full
  curator:
    enabled: false
    model: glm-5.3-flash
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.MemoryPromptInjectMode(); got != "full" {
		t.Errorf("prompt_inject=full not parsed, mode = %q", got)
	}
	if cfg.MemoryCuratorEnabled() {
		t.Error("curator enabled=false not parsed (defaults to true)")
	}
	if cfg.Memory.Curator.Model != "glm-5.3-flash" {
		t.Errorf("curator model = %q, want glm-5.3-flash", cfg.Memory.Curator.Model)
	}
}

// TestLoad_MemorySectionAbsentKeepsDefaults pins the defaults: pointer
// injection and an enabled curator.
func TestLoad_MemorySectionAbsentKeepsDefaults(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	if err := os.WriteFile(path, []byte("language: en\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.MemoryPromptInjectMode(); got != "pointer" {
		t.Errorf("default prompt_inject mode = %q, want pointer", got)
	}
	if !cfg.MemoryCuratorEnabled() {
		t.Error("curator must default to enabled")
	}
}
