package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test skill " + name + "\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCommand(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: test command " + name + "\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderProjectSourcesGate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(config.HomeDir(), ".ggcode", "skills", "userskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(config.HomeDir(), ".ggcode", "skills"), "userskill")

	projectDir := t.TempDir()
	writeSkill(t, filepath.Join(projectDir, ".ggcode", "skills"), "projskill")
	writeCommand(t, filepath.Join(projectDir, ".ggcode", "commands"), "projcmd")

	// Trusted / default: project sources loaded.
	full := NewLoaderWithOptions(projectDir, true).Load()
	if _, ok := full["projskill"]; !ok {
		t.Fatalf("project skill missing with includeProjectSources=true: %v", keys(full))
	}
	if _, ok := full["projcmd"]; !ok {
		t.Fatalf("project command missing with includeProjectSources=true")
	}
	if _, ok := full["userskill"]; !ok {
		t.Fatalf("user skill must always load")
	}

	// Restricted: project sources skipped, user sources intact.
	restricted := NewLoaderWithOptions(projectDir, false).Load()
	if _, ok := restricted["projskill"]; ok {
		t.Fatalf("restricted loader must skip project skill")
	}
	if _, ok := restricted["projcmd"]; ok {
		t.Fatalf("restricted loader must skip project command")
	}
	if _, ok := restricted["userskill"]; !ok {
		t.Fatalf("restricted loader must keep user skills")
	}
}

func TestManagerSetIncludeProjectSourcesReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	projectDir := t.TempDir()
	writeSkill(t, filepath.Join(projectDir, ".ggcode", "skills"), "late")

	m := NewManagerWithOptions(projectDir, false)
	if _, ok := m.Commands()["late"]; ok {
		t.Fatalf("restricted manager must not expose project skill")
	}
	if !m.SetIncludeProjectSources(true) {
		t.Fatalf("enabling project sources must trigger reload change")
	}
	if _, ok := m.Commands()["late"]; !ok {
		t.Fatalf("project skill must appear after SetIncludeProjectSources(true)")
	}
	if !m.SetIncludeProjectSources(false) {
		t.Fatalf("disabling project sources removes 'late', signature must change")
	}
	if _, ok := m.Commands()["late"]; ok {
		t.Fatalf("project skill must disappear after SetIncludeProjectSources(false)")
	}
}

func keys(m map[string]*Command) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
