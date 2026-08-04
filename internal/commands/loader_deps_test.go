package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFrontmatter_RequiresToolsAndDependencies(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "deploy-app")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" +
		"name: deploy-app\n" +
		"description: Deploy app\n" +
		"when_to_use: When deploying\n" +
		"requires-tools:\n" +
		"  - docker\n" +
		"  - kubectl\n" +
		"dependencies:\n" +
		"  - check-env\n" +
		"  - build-app\n" +
		"---\n\n" +
		"Deploy steps here"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cmds := loader.Load()
	cmd, ok := cmds["deploy-app"]
	if !ok {
		t.Fatal("expected deploy-app skill to be loaded")
	}
	if len(cmd.RequiresTools) != 2 {
		t.Errorf("expected 2 requires-tools, got %d: %v", len(cmd.RequiresTools), cmd.RequiresTools)
	}
	if cmd.RequiresTools[0] != "docker" || cmd.RequiresTools[1] != "kubectl" {
		t.Errorf("unexpected requires-tools: %v", cmd.RequiresTools)
	}
	if len(cmd.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d: %v", len(cmd.Dependencies), cmd.Dependencies)
	}
	if cmd.Dependencies[0] != "check-env" || cmd.Dependencies[1] != "build-app" {
		t.Errorf("unexpected dependencies: %v", cmd.Dependencies)
	}
}

func TestFrontmatter_NoDeps(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "simple")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: simple\ndescription: A simple skill\n---\n\nBody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cmds := loader.Load()
	cmd, ok := cmds["simple"]
	if !ok {
		t.Fatal("expected simple skill to be loaded")
	}
	if len(cmd.RequiresTools) != 0 {
		t.Errorf("expected 0 requires-tools, got %d", len(cmd.RequiresTools))
	}
	if len(cmd.Dependencies) != 0 {
		t.Errorf("expected 0 dependencies, got %d", len(cmd.Dependencies))
	}
}
