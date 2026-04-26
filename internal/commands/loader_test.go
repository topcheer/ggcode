package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))
	cmdDir := filepath.Join(tmpDir, ".ggcode", "commands")
	os.MkdirAll(cmdDir, 0755)

	os.WriteFile(filepath.Join(cmdDir, "review-pr.md"), []byte("Review the PR carefully"), 0644)
	os.WriteFile(filepath.Join(cmdDir, "test.md"), []byte("Run tests\nFocus on unit tests"), 0644)

	l := NewLoader(tmpDir)
	cmds := l.Load()

	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}

	if cmd, ok := cmds["review-pr"]; !ok {
		t.Error("missing review-pr command")
	} else if cmd.Description != "Review the PR carefully" {
		t.Errorf("wrong description: %q", cmd.Description)
	}

	if cmd, ok := cmds["test"]; !ok {
		t.Error("missing test command")
	} else if cmd.Template != "Run tests\nFocus on unit tests" {
		t.Errorf("wrong template: %q", cmd.Template)
	}
}

func TestCommand_Expand(t *testing.T) {
	c := &Command{
		Name:     "test",
		Template: "Review $FILE in $DIR",
	}
	result := c.Expand(map[string]string{"FILE": "main.go", "DIR": "/tmp"})
	if result != "Review main.go in /tmp" {
		t.Errorf("unexpected: %q", result)
	}
}

func TestLoad_SkillFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))
	skillDir := filepath.Join(tmpDir, ".ggcode", "skills", "deploy")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: Deploy service
description: Ship the current build
allowed-tools:
  - bash
argument-hint: <env>
arguments:
  - env
when_to_use: When the user wants to deploy
user-invocable: false
disable-model-invocation: true
context: project
---
Deploy from $DIR with $ARGS`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := NewLoader(tmpDir).Load()
	cmd, ok := cmds["deploy"]
	if !ok {
		t.Fatal("missing deploy skill")
	}
	if cmd.Title() != "Deploy service" {
		t.Fatalf("title = %q, want %q", cmd.Title(), "Deploy service")
	}
	if cmd.Description != "Ship the current build" {
		t.Fatalf("description = %q", cmd.Description)
	}
	if cmd.Source != SourceProject || cmd.LoadedFrom != LoadedFromSkills {
		t.Fatalf("unexpected source metadata: %s %s", cmd.Source, cmd.LoadedFrom)
	}
	if cmd.UserInvocable {
		t.Fatal("expected skill to be non-user-invocable")
	}
	if !cmd.DisableModelInvocation {
		t.Fatal("expected disable-model-invocation to be true")
	}
	if got := cmd.Expand(map[string]string{"DIR": "/repo", "ARGS": "prod"}); got != "Deploy from /repo with prod" {
		t.Fatalf("expanded skill = %q", got)
	}
}

func TestLoad_SharedAgentsSkills(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "skills", "shared", "SKILL.md"), []byte("Shared skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := NewLoader(tmpDir).Load()
	cmd, ok := cmds["shared"]
	if !ok {
		t.Fatal("missing shared skill from ~/.agents/skills")
	}
	if cmd.Template != "Shared skill" {
		t.Fatalf("template = %q", cmd.Template)
	}
	if cmd.Source != SourceUser || cmd.LoadedFrom != LoadedFromSkills {
		t.Fatalf("unexpected source metadata: %s %s", cmd.Source, cmd.LoadedFrom)
	}
}

func TestLoad_GGCodeSkillsOverrideSharedAgentsSkills(t *testing.T) {
	tmpDir := t.TempDir()
	home := filepath.Join(tmpDir, "home")
	t.Setenv("HOME", home)
	sharedDir := filepath.Join(home, ".agents", "skills", "deploy")
	ggcodeDir := filepath.Join(home, ".ggcode", "skills", "deploy")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ggcodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "SKILL.md"), []byte("Shared deploy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ggcodeDir, "SKILL.md"), []byte("GGCode deploy"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmds := NewLoader(tmpDir).Load()
	cmd, ok := cmds["deploy"]
	if !ok {
		t.Fatal("missing deploy skill")
	}
	if cmd.Template != "GGCode deploy" {
		t.Fatalf("template = %q, want %q", cmd.Template, "GGCode deploy")
	}
}
