package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nDo the thing.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectAgentsSkillsDirLoaded(t *testing.T) {
	project := t.TempDir()
	agentsDir := filepath.Join(project, ".agents", "skills")
	writeSkill(t, agentsDir, "deploy-std", "open-standard project skill")

	cmds := NewLoader(project).Load()
	cmd, ok := cmds["deploy-std"]
	if !ok {
		t.Fatalf("project-level .agents/skills/ skill not loaded; loaded=%d", len(cmds))
	}
	if cmd.Source != SourceProject || cmd.LoadedFrom != LoadedFromSkills {
		t.Fatalf("deploy-std source=%q loadedFrom=%q, want project/skills", cmd.Source, cmd.LoadedFrom)
	}
}

func TestGgcodeSkillsOverridesAgentsSkills(t *testing.T) {
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, ".agents", "skills"), "deploy", "open standard variant")
	writeSkill(t, filepath.Join(project, ".ggcode", "skills"), "deploy", "ggcode native variant")

	cmd := NewLoader(project).Load()["deploy"]
	if cmd == nil {
		t.Fatal("deploy skill not loaded")
	}
	if cmd.Description != "ggcode native variant" {
		t.Fatalf(".ggcode/skills should override .agents/skills for the same name, got %q", cmd.Description)
	}
}
