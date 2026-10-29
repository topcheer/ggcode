package config

import (
	"os"
	"path/filepath"
	"testing"
)

// zz_issue4267_test.go - a legacy `default_mode: plan` persisted by a
// pre-two-mode-refactor binary must not brick every CLI entry point: Load
// normalizes it to supervised instead of failing Validate.

func loadWithDefaultMode(t *testing.T, mode string) (*Config, error) {
	t.Helper()
	withTestHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
default_mode: ` + mode + `
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return Load(path)
}

func TestLoad_DefaultModePlanNormalizes_Issue4267(t *testing.T) {
	cfg, err := loadWithDefaultMode(t, "plan")
	if err != nil {
		t.Fatalf("Load with legacy default_mode: plan must not fail: %v", err)
	}
	if cfg.DefaultMode != "supervised" {
		t.Fatalf("DefaultMode = %q, want normalized %q", cfg.DefaultMode, "supervised")
	}
}

func TestLoad_DefaultModePlanCaseInsensitive_Issue4267(t *testing.T) {
	cfg, err := loadWithDefaultMode(t, "PLAN")
	if err != nil {
		t.Fatalf("Load with legacy default_mode: PLAN must not fail: %v", err)
	}
	if cfg.DefaultMode != "supervised" {
		t.Fatalf("DefaultMode = %q, want normalized %q", cfg.DefaultMode, "supervised")
	}
}
