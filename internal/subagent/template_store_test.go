package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTemplateStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	s := &TemplateStore{dir: dir}

	tmpl := NamedAgentTemplate{
		Name:         "code-reviewer",
		Description:  "Reviews code for bugs",
		SystemPrompt: "You are a code reviewer.",
		Tools:        []string{"read_file", "grep"},
		Model:        "",
	}
	if err := s.Save(tmpl); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load("code-reviewer")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "code-reviewer" {
		t.Errorf("name = %q", loaded.Name)
	}
	if loaded.SystemPrompt != "You are a code reviewer." {
		t.Errorf("prompt = %q", loaded.SystemPrompt)
	}
	if loaded.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}

	tmpl.SystemPrompt = "Updated prompt"
	if err := s.Save(tmpl); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	updated, _ := s.Load("code-reviewer")
	if updated.SystemPrompt != "Updated prompt" {
		t.Errorf("not updated: %q", updated.SystemPrompt)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt) && !updated.UpdatedAt.Equal(updated.CreatedAt) {
		t.Error("UpdatedAt should be >= CreatedAt")
	}

	list, _ = s.List()
	if len(list) != 1 {
		t.Errorf("after update len = %d", len(list))
	}

	s.Save(NamedAgentTemplate{Name: "test-writer", Description: "d", SystemPrompt: "p"})

	list, _ = s.List()
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Name != "code-reviewer" {
		t.Errorf("first = %q", list[0].Name)
	}

	if err := s.Delete("code-reviewer"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, _ = s.List()
	if len(list) != 1 {
		t.Errorf("after delete len = %d", len(list))
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"code-reviewer", "code-reviewer"},
		{"My Agent", "my_agent"},
		{"a/b\\c:d", "a_b_c_d"},
	}
	for _, tt := range tests {
		got := sanitizeName(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNewTemplateStore_Path(t *testing.T) {

	s := NewTemplateStore("/tmp/some-workspace")
	if s.dir == "" {
		t.Error("dir should not be empty")
	}

	s2 := NewTemplateStore("/tmp/some-workspace")
	if s.dir != s2.dir {
		t.Error("same workspace should produce same dir")
	}
}

func TestSha256Hash(t *testing.T) {
	h := sha256.Sum256([]byte("test"))
	want := hex.EncodeToString(h[:])[:16]
	got := sha256Hash("test")
	if got != want {
		t.Errorf("sha256Hash = %q, want %q", got, want)
	}
}

func TestTemplateStore_NameCollision(t *testing.T) {
	dir := t.TempDir()
	store := NewTemplateStore(dir)

	a := NamedAgentTemplate{Name: "Code Reviewer", Description: "first", SystemPrompt: "p1"}
	if err := store.Save(a); err != nil {
		t.Fatalf("save first: %v", err)
	}

	b := NamedAgentTemplate{Name: "code_reviewer", Description: "second", SystemPrompt: "p2"}
	if err := store.Save(b); err == nil {
		t.Fatal("expected collision error, got nil (silent overwrite)")
	}

	got, err := store.Load("Code Reviewer")
	if err != nil {
		t.Fatalf("load original: %v", err)
	}
	if got.SystemPrompt != "p1" {
		t.Errorf("original overwritten: prompt=%q", got.SystemPrompt)
	}

	if _, err := store.Load("code_reviewer"); err == nil {
		t.Error("loading colliding name should error, not return the wrong template")
	}

	a.SystemPrompt = "p1-updated"
	if err := store.Save(a); err != nil {
		t.Fatalf("legit update: %v", err)
	}
	got2, _ := store.Load("Code Reviewer")
	if got2.SystemPrompt != "p1-updated" || !got2.CreatedAt.Equal(got.CreatedAt) {
		t.Errorf("update broken: prompt=%q created=%v", got2.SystemPrompt, got2.CreatedAt)
	}
}

func TestSaveCaseRenameNotCollision(t *testing.T) {
	dir := t.TempDir()
	ts := &TemplateStore{dir: dir}
	orig := &NamedAgentTemplate{Name: "code reviewer", Description: "d"}
	if err := ts.Save(*orig); err != nil {
		t.Fatal(err)
	}

	renamed := &NamedAgentTemplate{Name: "  Code Reviewer  ", Description: "d2"}
	if err := ts.Save(*renamed); err != nil {
		t.Fatalf("case-only rename should update, got: %v", err)
	}

	other := &NamedAgentTemplate{Name: "code/reviewer", Description: "x"}
	if err := ts.Save(*other); err == nil {
		t.Fatal("different name with same sanitized filename must collide")
	}
}

func TestTemplateStore_DeleteNormalized(t *testing.T) {
	dir := t.TempDir()
	s := &TemplateStore{dir: dir}
	if err := s.Save(NamedAgentTemplate{Name: "Code Reviewer", SystemPrompt: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete("  code reviewer "); err != nil {
		t.Fatalf("Delete with normalized casing must succeed, got: %v", err)
	}
	if _, err := s.Load("Code Reviewer"); err == nil {
		t.Fatal("template must be gone after normalized delete")
	}
}
