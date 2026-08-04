package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// stubReloader implements SkillReloader for testing.
type stubReloader struct {
	cmds map[string]*commands.Command
}

func (s *stubReloader) Reload() bool { return true }
func (s *stubReloader) Get(name string) (*commands.Command, bool) {
	cmd, ok := s.cmds[name]
	return cmd, ok
}

func TestCreateSkill_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	mgr := &commands.Manager{}
	_ = mgr
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{cmds: map[string]*commands.Command{}},
		WorkingDir: dir,
	}

	input, _ := json.Marshal(map[string]string{
		"name":        "deploy-app",
		"description": "Deploy the application to production",
		"content":     "Run deployment steps here",
		"when_to_use": "When deploying to production",
		"scope":       "project",
	})

	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	// Verify file was created
	skillFile := filepath.Join(dir, ".ggcode", "skills", "deploy-app", "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Fatal("expected SKILL.md to be created")
	}

	data, _ := os.ReadFile(skillFile)
	content := string(data)
	if !contains(content, "name: deploy-app") {
		t.Errorf("expected frontmatter name, got: %s", content)
	}
	if !contains(content, "Run deployment steps here") {
		t.Errorf("expected body content, got: %s", content)
	}
}

func TestCreateSkill_Execute_InvalidName(t *testing.T) {
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{cmds: map[string]*commands.Command{}},
		WorkingDir: t.TempDir(),
	}

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"uppercase", "MySkill"},
		{"special chars", "my skill!"},
		{"path traversal", "../escape"},
		{"too long", string(make([]byte, 81))},
	}

	for _, tt := range tests {
		// Fix: use properly encoded name
		var input json.RawMessage
		if tt.name == "too long" {
			longName := makeLongName(81)
			input, _ = json.Marshal(map[string]string{"name": longName, "description": "d", "content": "c"})
		} else {
			input, _ = json.Marshal(map[string]string{"name": tt.input, "description": "d", "content": "c"})
		}
		result, _ := tool.Execute(t.Context(), input)
		if !result.IsError {
			t.Errorf("expected error for %s, got success", tt.name)
		}
	}
}

func TestCreateSkill_Execute_AlreadyExists(t *testing.T) {
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{
			cmds: map[string]*commands.Command{
				"existing": {Name: "existing"},
			},
		},
		WorkingDir: t.TempDir(),
	}

	input, _ := json.Marshal(map[string]string{
		"name":        "existing",
		"description": "d",
		"content":     "c",
	})

	result, _ := tool.Execute(t.Context(), input)
	if !result.IsError {
		t.Fatal("expected error for duplicate skill name")
	}
}

func TestCreateSkill_Execute_GlobalScope(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	tool := CreateSkillTool{
		CommandMgr: &stubReloader{cmds: map[string]*commands.Command{}},
		WorkingDir: t.TempDir(),
	}

	input, _ := json.Marshal(map[string]string{
		"name":        "global-skill",
		"description": "A global skill",
		"content":     "Global content",
		"scope":       "global",
	})

	result, _ := tool.Execute(t.Context(), input)
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content)
	}

	skillFile := filepath.Join(dir, ".ggcode", "skills", "global-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Fatal("expected global SKILL.md to be created")
	}
}

func TestCreateSkill_Clone(t *testing.T) {
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{},
		WorkingDir: "/tmp",
	}
	cloned := tool.Clone()
	if cloned.(CreateSkillTool).WorkingDir != "/tmp" {
		t.Error("clone should preserve WorkingDir")
	}
}

func TestValidateSkillName(t *testing.T) {
	valid := []string{"deploy", "my-skill", "test123", "a_b"}
	for _, name := range valid {
		if err := validateSkillName(name); err != nil {
			t.Errorf("expected valid for %q: %v", name, err)
		}
	}

	invalid := []string{"", "UPPER", "has space", "../bad", "a!b"}
	for _, name := range invalid {
		if err := validateSkillName(name); err == nil {
			t.Errorf("expected invalid for %q", name)
		}
	}
}

func TestCreateSkill_Execute_WithDependencies(t *testing.T) {
	dir := t.TempDir()
	tool := CreateSkillTool{
		CommandMgr: &stubReloader{cmds: map[string]*commands.Command{}},
		WorkingDir: dir,
	}

	input, _ := json.Marshal(map[string]interface{}{
		"name":           "deploy-app",
		"description":    "Deploy the application",
		"content":        "Run deployment steps",
		"requires_tools": []string{"docker", "kubectl"},
		"dependencies":   []string{"check-env", "build-app"},
		"scope":          "project",
	})

	result, err := tool.Execute(t.Context(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content)
	}

	skillFile := filepath.Join(dir, ".ggcode", "skills", "deploy-app", "SKILL.md")
	data, _ := os.ReadFile(skillFile)
	content := string(data)

	if !contains(content, "requires-tools:") {
		t.Errorf("expected requires-tools in frontmatter, got: %s", content)
	}
	if !contains(content, "docker") || !contains(content, "kubectl") {
		t.Errorf("expected docker and kubectl in requires-tools, got: %s", content)
	}
	if !contains(content, "dependencies:") {
		t.Errorf("expected dependencies in frontmatter, got: %s", content)
	}
	if !contains(content, "check-env") || !contains(content, "build-app") {
		t.Errorf("expected check-env and build-app in dependencies, got: %s", content)
	}
}

func makeLongName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
