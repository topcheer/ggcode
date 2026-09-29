package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDraft(t *testing.T, projectDir, fileName, frontmatter string) string {
	t.Helper()
	dir := SkillDraftsDir(projectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fileName)
	content := "---\n" + frontmatter + "---\n\n# draft body\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListSkillDrafts(t *testing.T) {
	project := t.TempDir()
	if drafts := ListSkillDrafts(project); drafts != nil {
		t.Fatalf("expected nil for missing drafts dir, got %v", drafts)
	}
	writeDraft(t, project, "build-20260929-run1.md",
		"name: build-20260929-run1\ndescription: fix the failing build\nprocedure-hash: abc123\nstatus: draft\n")
	writeDraft(t, project, "notes.txt", "ignored") // non-.md files are skipped

	drafts := ListSkillDrafts(project)
	if len(drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d: %+v", len(drafts), drafts)
	}
	d := drafts[0]
	if d.Name != "build-20260929-run1" {
		t.Errorf("Name = %q", d.Name)
	}
	if d.Description != "fix the failing build" {
		t.Errorf("Description = %q", d.Description)
	}
	if d.Status != "draft" {
		t.Errorf("Status = %q", d.Status)
	}
	if !strings.HasSuffix(d.Path, filepath.Join("skill-drafts", "build-20260929-run1.md")) {
		t.Errorf("Path = %q", d.Path)
	}
}

func TestActivateSkillDraft(t *testing.T) {
	project := t.TempDir()
	draftPath := writeDraft(t, project, "build-20260929-run1.md",
		"name: build-20260929-run1\ndescription: fix the failing build\nstatus: draft\n")

	name, err := ActivateSkillDraft(project, draftPath)
	if err != nil {
		t.Fatalf("ActivateSkillDraft: %v", err)
	}
	if name != "build-20260929-run1" {
		t.Fatalf("name = %q", name)
	}

	activated := filepath.Join(project, ".ggcode", "skills", name, "SKILL.md")
	data, err := os.ReadFile(activated)
	if err != nil {
		t.Fatalf("activated skill missing: %v", err)
	}
	if !strings.Contains(string(data), "status: draft") {
		t.Error("activated skill must preserve draft content byte-for-byte")
	}
	if _, err := os.Stat(draftPath); !os.IsNotExist(err) {
		t.Error("draft file should be removed after activation")
	}

	// The normal loader must now pick the skill up from the project registry.
	if _, ok := NewLoader(project).Load()[name]; !ok {
		t.Error("loader does not see the activated skill")
	}
}

func TestActivateSkillDraftNoClobber(t *testing.T) {
	project := t.TempDir()
	existing := filepath.Join(project, ".ggcode", "skills", "my-skill")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := writeDraft(t, project, "d1.md", "name: my-skill\ndescription: dup\n")

	if _, err := ActivateSkillDraft(project, draftPath); err == nil {
		t.Fatal("expected error when target skill already exists")
	}
	if _, err := os.Stat(draftPath); err != nil {
		t.Error("draft must remain untouched on failed activation")
	}
}

func TestActivateSkillDraftSanitizesName(t *testing.T) {
	project := t.TempDir()
	draftPath := writeDraft(t, project, "d1.md", "name: ../My Skill/evil\ndescription: x\n")

	name, err := ActivateSkillDraft(project, draftPath)
	if err != nil {
		t.Fatalf("ActivateSkillDraft: %v", err)
	}
	if strings.ContainsAny(name, "/\\ ") || strings.Contains(name, "..") {
		t.Fatalf("unsanitized name %q", name)
	}
	if _, err := os.Stat(filepath.Join(project, ".ggcode", "skills", name, "SKILL.md")); err != nil {
		t.Fatalf("sanitized skill dir missing: %v", err)
	}
}

func TestActivateSkillDraftNameFromFilename(t *testing.T) {
	project := t.TempDir()
	// No frontmatter at all: fall back to the sanitized file base name.
	dir := SkillDraftsDir(project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	draftPath := filepath.Join(dir, "plain draft.md")
	if err := os.WriteFile(draftPath, []byte("# no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := ActivateSkillDraft(project, draftPath)
	if err != nil {
		t.Fatalf("ActivateSkillDraft: %v", err)
	}
	if name != "plain-draft" {
		t.Fatalf("name = %q, want plain-draft", name)
	}
	if _, err := os.Stat(filepath.Join(project, ".ggcode", "skills", name, "SKILL.md")); err != nil {
		t.Fatalf("skill dir missing: %v", err)
	}
}

func TestSanitizeSkillDirName(t *testing.T) {
	cases := map[string]string{
		"build-20260929-run1": "build-20260929-run1",
		"  spaced name  ":     "spaced-name",
		"a/b\\c:d*e?f":        "a-b-c-d-e-f",
		"...":                 "",
		"":                    "",
	}
	for in, want := range cases {
		if got := sanitizeSkillDirName(in); got != want {
			t.Errorf("sanitizeSkillDirName(%q) = %q, want %q", in, got, want)
		}
	}
}
