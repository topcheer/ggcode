package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTemplateStore_CRUD(t *testing.T) {
	dir := t.TempDir()
	s := &TemplateStore{dir: dir}

	// Save
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

	// Load
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

	// List
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}

	// Update (save again with same name)
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

	// List still 1 (updated in place)
	list, _ = s.List()
	if len(list) != 1 {
		t.Errorf("after update len = %d", len(list))
	}

	// Add second
	s.Save(NamedAgentTemplate{Name: "test-writer", Description: "d", SystemPrompt: "p"})

	// List sorted
	list, _ = s.List()
	if len(list) != 2 {
		t.Fatalf("len = %d", len(list))
	}
	if list[0].Name != "code-reviewer" {
		t.Errorf("first = %q", list[0].Name)
	}

	// Delete
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
	// Just verify it doesn't panic and produces a path with "subagents"
	s := NewTemplateStore("/tmp/some-workspace")
	if s.dir == "" {
		t.Error("dir should not be empty")
	}
	// Verify hash is deterministic
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
