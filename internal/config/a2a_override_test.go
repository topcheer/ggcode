package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadA2AOverrideNotExist(t *testing.T) {
	dir := t.TempDir()
	ov := LoadA2AOverride(dir)
	if ov != nil {
		t.Error("expected nil for nonexistent override")
	}
}

func TestLoadA2AOverrideFromFile(t *testing.T) {
	dir := t.TempDir()
	ggcodeDir := filepath.Join(dir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)

	yamlContent := `disabled: true
port: 9999
host: "0.0.0.0"
api_key: "instance-key"
max_tasks: 10
task_timeout: "10m"
`
	if err := os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}

	ov := LoadA2AOverride(dir)
	if ov == nil {
		t.Fatal("expected non-nil override")
	}
	if !ov.Disabled {
		t.Error("expected disabled=true")
	}
	if ov.Port != 9999 {
		t.Errorf("expected port=9999, got %d", ov.Port)
	}
	if ov.Host != "0.0.0.0" {
		t.Errorf("expected host=0.0.0.0, got %s", ov.Host)
	}
	if ov.APIKey != "instance-key" {
		t.Errorf("expected api_key=instance-key, got %s", ov.APIKey)
	}
	if ov.MaxTasks != 10 {
		t.Errorf("expected max_tasks=10, got %d", ov.MaxTasks)
	}
	if ov.TaskTimeout != "10m" {
		t.Errorf("expected task_timeout=10m, got %s", ov.TaskTimeout)
	}
}

func TestMergeA2AConfig(t *testing.T) {
	base := &A2AConfig{
		Port:        0,
		Host:        "127.0.0.1",
		APIKey:      "global-key",
		MaxTasks:    5,
		TaskTimeout: "5m",
	}

	override := &A2AConfig{
		Port:     8080,
		APIKey:   "instance-key",
		MaxTasks: 20,
	}

	MergeA2AConfig(base, override)

	if base.Port != 8080 {
		t.Errorf("expected port=8080, got %d", base.Port)
	}
	if base.Host != "127.0.0.1" {
		t.Errorf("host should stay 127.0.0.1, got %s", base.Host)
	}
	if base.APIKey != "instance-key" {
		t.Errorf("expected api_key=instance-key, got %s", base.APIKey)
	}
	if base.MaxTasks != 20 {
		t.Errorf("expected max_tasks=20, got %d", base.MaxTasks)
	}
	if base.TaskTimeout != "5m" {
		t.Errorf("task_timeout should stay 5m, got %s", base.TaskTimeout)
	}
}

func TestMergeA2AConfigNilOverride(t *testing.T) {
	base := &A2AConfig{Port: 1234, APIKey: "key"}
	MergeA2AConfig(base, nil)
	if base.Port != 1234 || base.APIKey != "key" {
		t.Error("nil override should not change base")
	}
}

func TestMergeA2AConfigAuth(t *testing.T) {
	base := &A2AConfig{}
	override := &A2AConfig{
		Auth: A2AAuthConfig{
			OAuth2: &A2AOAuth2Config{
				Provider: "github",
				ClientID: "test-id",
			},
		},
	}

	MergeA2AConfig(base, override)
	if base.Auth.OAuth2 == nil {
		t.Fatal("expected OAuth2 override")
	}
	if base.Auth.OAuth2.Provider != "github" {
		t.Errorf("expected provider=github, got %s", base.Auth.OAuth2.Provider)
	}
}

func TestLoadA2AOverrideInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	ggcodeDir := filepath.Join(dir, ".ggcode")
	os.MkdirAll(ggcodeDir, 0755)

	if err := os.WriteFile(filepath.Join(ggcodeDir, "a2a.yaml"), []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	ov := LoadA2AOverride(dir)
	if ov != nil {
		t.Error("expected nil for invalid YAML")
	}
}
