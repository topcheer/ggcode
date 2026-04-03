package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name       string
		basePrompt string
		workingDir string
		toolNames  []string
		gitStatus  string
		customCmds []string
		want       []string // substrings that must appear
	}{
		{
			name:       "default prompt",
			basePrompt: "",
			workingDir: "/tmp",
			toolNames:  []string{"read_file", "write_file"},
			want:       []string{"ggcode", "read_file", "write_file", "/tmp"},
		},
		{
			name:       "custom prompt",
			basePrompt: "You are a helper.",
			workingDir: "/home/user",
			toolNames:  []string{"bash"},
			want:       []string{"helper", "bash", "/home/user"},
		},
		{
			name:       "with git status",
			basePrompt: "",
			workingDir: "/tmp",
			toolNames:  []string{"git_status"},
			gitStatus:  "main, 2 commits ahead",
			want:       []string{"main", "2 commits ahead"},
		},
		{
			name:       "with custom commands",
			basePrompt: "",
			workingDir: "/tmp",
			toolNames:  []string{"bash"},
			customCmds: []string{"/deploy", "/build"},
			want:       []string{"/deploy", "/build"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSystemPrompt(tt.basePrompt, tt.workingDir, tt.toolNames, tt.gitStatus, tt.customCmds)
			for _, substr := range tt.want {
				if !contains(result, substr) {
					t.Errorf("BuildSystemPrompt() missing %q in output", substr)
				}
			}
		})
	}
}

func TestLoad_NonExistent(t *testing.T) {
	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Errorf("expected default system prompt, got %q", cfg.SystemPrompt)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(path, []byte(":\n  - invalid: ["), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
system_prompt: "Custom prompt"
allowed_dirs:
  - /tmp
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SystemPrompt != "Custom prompt" {
		t.Errorf("expected 'Custom prompt', got %q", cfg.SystemPrompt)
	}
	if len(cfg.AllowedDirs) != 1 || cfg.AllowedDirs[0] != "/tmp" {
		t.Errorf("expected [/tmp], got %v", cfg.AllowedDirs)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.SystemPrompt == "" {
		t.Error("DefaultConfig() has empty system prompt")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
