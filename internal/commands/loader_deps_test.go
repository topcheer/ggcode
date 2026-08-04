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

func TestFrontmatter_Version(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "versioned")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\n" +
		"name: versioned\n" +
		"description: A versioned skill\n" +
		"version: \"1.2.0\"\n" +
		"dependencies:\n" +
		"  - base-skill@>=1.0.0\n" +
		"  - exact-skill@2.0.0\n" +
		"---\n\nVersioned body"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	cmds := loader.Load()
	cmd, ok := cmds["versioned"]
	if !ok {
		t.Fatal("expected versioned skill to be loaded")
	}
	if cmd.Version != "1.2.0" {
		t.Errorf("expected version 1.2.0, got %q", cmd.Version)
	}
	if len(cmd.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d", len(cmd.Dependencies))
	}
	dep1 := ParseDependency(cmd.Dependencies[0])
	if dep1.Name != "base-skill" || dep1.Op != ">=" || dep1.Version != "1.0.0" {
		t.Errorf("unexpected parsed dep1: %+v", dep1)
	}
	dep2 := ParseDependency(cmd.Dependencies[1])
	if dep2.Name != "exact-skill" || dep2.Op != "==" || dep2.Version != "2.0.0" {
		t.Errorf("unexpected parsed dep2: %+v", dep2)
	}
}
