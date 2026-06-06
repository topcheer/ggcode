package wailskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
)

func TestBuildWailsSystemPromptIncludesSkills(t *testing.T) {
	projectDir := t.TempDir()
	skillsDir := filepath.Join(projectDir, ".ggcode", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: investigate
description: Investigation workflow
when_to_use: Use when debugging behavior.
---
Skill body`
	if err := os.WriteFile(filepath.Join(skillsDir, "investigate.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	commandMgr := commands.NewManager(projectDir)
	prompt := buildWailsSystemPrompt(config.DefaultConfig(), projectDir, permission.AutoMode, memory.NewAutoMemory(), memory.NewProjectAutoMemory(projectDir), commandMgr)
	if !strings.Contains(prompt, "## Skills") || !strings.Contains(prompt, "Available skills:") {
		t.Fatalf("expected skills section in prompt, got %q", prompt)
	}
}

func TestBuildWailsSystemPromptMatchesSharedSkillsPrompt(t *testing.T) {
	projectDir := t.TempDir()
	commandMgr := commands.NewManager(projectDir)
	skillsPrompt := agentruntime.BuildSkillsSystemPrompt(commandMgr.List())
	prompt := buildWailsSystemPrompt(config.DefaultConfig(), projectDir, permission.AutoMode, memory.NewAutoMemory(), memory.NewProjectAutoMemory(projectDir), commandMgr)
	if skillsPrompt == "" {
		t.Skip("no visible skills in this environment")
	}
	if !strings.Contains(prompt, skillsPrompt) {
		t.Fatalf("expected prompt to contain shared skills prompt")
	}
}
