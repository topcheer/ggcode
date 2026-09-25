package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

// TestSkillsSystemPromptHidesUnactivatedGatedSkills pins the prompt-side
// discovery filter: a skill declaring `paths:` frontmatter is absent from the
// startup system prompt until the agent touches a matching file, and appears
// once activated (the prompt is rebuilt from the passed slice on each call).
func TestSkillsSystemPromptHidesUnactivatedGatedSkills(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".ggcode", "skills", "proto-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: proto-helper\ndescription: Regenerate protobuf bindings\npaths:\n  - \"**/*.proto\"\n---\n\nBody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	commands.ResetSkillGateForTest()
	t.Cleanup(commands.ResetSkillGateForTest)

	m := commands.NewManager(dir)
	skills := m.List()

	prompt := BuildSkillsSystemPrompt(skills)
	if strings.Contains(prompt, "proto-helper") {
		t.Fatal("unactivated path-gated skill must be absent from the system prompt")
	}

	commands.NoteTouchedPaths("proto/api.proto")
	prompt = BuildSkillsSystemPrompt(m.List())
	if !strings.Contains(prompt, "proto-helper") {
		t.Fatal("activated path-gated skill must appear in the system prompt")
	}
}
