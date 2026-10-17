package subagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// zz_r555_project_templates_test.go - project-level named agent templates
// (<workspace>/.ggcode/agents/*.json), mirroring the .claude/agents
// agents-as-code convention. Project templates shadow same-named user
// templates; Save/Delete refuse project-scoped names so user-side writes can
// never be silently masked by the committed definition.

// newProjectStore builds a store whose workspace has a .ggcode/agents dir,
// plus one user-level template pre-saved through the normal API.
func newProjectStore(t *testing.T) (*TemplateStore, string) {
	t.Helper()
	withTestHome(t) // user library isolated under a temp HOME
	ws := t.TempDir()
	// normalizeWorkspacePath resolves symlinks (EvalSymlinks), and on macOS
	// t.TempDir() lives under /var -> /private/var; compare against the
	// resolved form.
	wsResolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(ws, ".ggcode", "agents")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewTemplateStore(ws)
	if s.projectDir != filepath.Join(wsResolved, ".ggcode", "agents") {
		t.Fatalf("projectDir = %q, want %q", s.projectDir, filepath.Join(wsResolved, ".ggcode", "agents"))
	}
	if err := s.Save(NamedAgentTemplate{Name: "user-only", SystemPrompt: "u"}); err != nil {
		t.Fatal(err)
	}
	return s, projectDir
}

func writeProjectTemplate(t *testing.T, projectDir, name, prompt string) {
	t.Helper()
	tpl := NamedAgentTemplate{Name: name, SystemPrompt: prompt, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	data, err := json.MarshalIndent(tpl, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, sanitizeName(name)+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ① Project templates appear in Load and List.
func TestProjectTemplate_LoadAndList(t *testing.T) {
	s, projectDir := newProjectStore(t)
	writeProjectTemplate(t, projectDir, "reviewer", "project prompt")

	got, err := s.Load("reviewer")
	if err != nil {
		t.Fatalf("Load project template: %v", err)
	}
	if got.SystemPrompt != "project prompt" {
		t.Fatalf("Load returned %q, want project prompt", got.SystemPrompt)
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tpl := range list {
		names[tpl.Name] = true
	}
	if !names["reviewer"] || !names["user-only"] {
		t.Fatalf("List must contain project and user templates, got %v", names)
	}
}

// ② A same-named project template shadows the user-level one.
func TestProjectTemplate_ShadowsUserTemplate(t *testing.T) {
	s, projectDir := newProjectStore(t)
	// Same sanitized name at both levels.
	if err := s.Save(NamedAgentTemplate{Name: "Reviewer", SystemPrompt: "user prompt"}); err != nil {
		t.Fatal(err)
	}
	writeProjectTemplate(t, projectDir, "reviewer", "project prompt")

	got, err := s.Load("reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemPrompt != "project prompt" {
		t.Fatalf("project template must shadow user template, got %q", got.SystemPrompt)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, tpl := range list {
		if strings.EqualFold(strings.TrimSpace(tpl.Name), "reviewer") {
			count++
			if tpl.SystemPrompt != "project prompt" {
				t.Fatalf("List must surface the project shadow, got %q", tpl.SystemPrompt)
			}
		}
	}
	if count != 1 {
		t.Fatalf("same-named project+user template must merge to 1 entry, got %d", count)
	}
}

// ③ Save refuses to (over)write a project-scoped name.
func TestProjectTemplate_SaveRefused(t *testing.T) {
	s, projectDir := newProjectStore(t)
	writeProjectTemplate(t, projectDir, "reviewer", "project prompt")

	err := s.Save(NamedAgentTemplate{Name: "reviewer", SystemPrompt: "hijack"})
	if err == nil {
		t.Fatal("Save must refuse a project-scoped name")
	}
	if !strings.Contains(err.Error(), "project-scoped") {
		t.Fatalf("error should point at the project file, got: %v", err)
	}
	// The committed file must be untouched.
	got, lerr := s.Load("reviewer")
	if lerr != nil || got.SystemPrompt != "project prompt" {
		t.Fatalf("project file must be untouched after refused Save: %v %+v", lerr, got)
	}
	// Non-project names still save fine.
	if err := s.Save(NamedAgentTemplate{Name: "another", SystemPrompt: "ok"}); err != nil {
		t.Fatalf("Save of a non-project name must still work: %v", err)
	}
}

// ④ Delete refuses a project-scoped name but still deletes user templates.
func TestProjectTemplate_DeleteRefused(t *testing.T) {
	s, projectDir := newProjectStore(t)
	writeProjectTemplate(t, projectDir, "reviewer", "project prompt")

	if err := s.Delete("reviewer"); err == nil {
		t.Fatal("Delete must refuse a project-scoped name")
	} else if !strings.Contains(err.Error(), "project-scoped") {
		t.Fatalf("error should point at the project file, got: %v", err)
	}
	if _, err := s.Load("reviewer"); err != nil {
		t.Fatalf("project template must survive refused Delete: %v", err)
	}
	if err := s.Delete("user-only"); err != nil {
		t.Fatalf("Delete of a user template must still work: %v", err)
	}
}

// ⑤ Without a .ggcode/agents directory the store behaves exactly as before.
func TestProjectTemplate_NoProjectDirRegression(t *testing.T) {
	withTestHome(t)
	ws := t.TempDir() // no .ggcode/agents
	s := NewTemplateStore(ws)
	if s.projectDir != "" {
		t.Fatalf("projectDir must be empty without .ggcode/agents, got %q", s.projectDir)
	}
	if err := s.Save(NamedAgentTemplate{Name: "solo", SystemPrompt: "u"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("solo"); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Name != "solo" {
		t.Fatalf("home-only store behavior regressed: %v %v", list, err)
	}
	if err := s.Delete("solo"); err != nil {
		t.Fatal(err)
	}
	if list, err = s.List(); err != nil || len(list) != 0 {
		t.Fatalf("delete regressed: %v %v", list, err)
	}
}
