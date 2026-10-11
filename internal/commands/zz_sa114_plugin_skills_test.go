package commands

// sa-114: plugin-shipped skills (agent-plugins.org) load from
// <project>/.agent-plugins/<dir>/skills/ and are overridable by native dirs.

import (
	"os"
	"path/filepath"
	"testing"
)

func writePluginWithSkill(t *testing.T, project, slug, skillName string) {
	t.Helper()
	pluginDir := filepath.Join(project, ".agent-plugins", slug)
	skillDir := filepath.Join(pluginDir, "skills", skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name": "`+slug+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + skillName + "\ndescription: plugin skill\n---\n\nFrom plugin.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPluginSkillLoaded(t *testing.T) {
	project := t.TempDir()
	writePluginWithSkill(t, project, "toolkit", "lint-all")

	cmds := NewLoader(project).Load()
	cmd, ok := cmds["lint-all"]
	if !ok {
		t.Fatalf("plugin skill not loaded; loaded=%d", len(cmds))
	}
	if cmd.Source != SourceProject || cmd.LoadedFrom != LoadedFromSkills {
		t.Fatalf("lint-all source=%q loadedFrom=%q", cmd.Source, cmd.LoadedFrom)
	}
}

func TestNativeSkillOverridesPluginSkill(t *testing.T) {
	project := t.TempDir()
	writePluginWithSkill(t, project, "toolkit", "deploy")
	// Same name in project .ggcode/skills: native wins (plugin targets are
	// prepended; later targets override earlier ones).
	nativeDir := filepath.Join(project, ".ggcode", "skills", "deploy")
	if err := os.MkdirAll(nativeDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: deploy\ndescription: native variant\n---\n\nNative.\n"
	if err := os.WriteFile(filepath.Join(nativeDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := NewLoader(project).Load()["deploy"]
	if cmd == nil {
		t.Fatal("deploy not loaded")
	}
	if cmd.Description != "native variant" {
		t.Fatalf("native .ggcode/skills must override plugin skill, got %q", cmd.Description)
	}
}

func TestManifestlessPluginDirIgnored(t *testing.T) {
	project := t.TempDir()
	// skills/ directory under .agent-plugins without plugin.json: not a
	// plugin package, contributes nothing.
	skillDir := filepath.Join(project, ".agent-plugins", "stray", "skills", "ghost")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: ghost\ndescription: should not load\n---\n\nNope.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := NewLoader(project).Load()["ghost"]; ok {
		t.Fatal("skills under a manifest-less plugin dir must not load")
	}
}
