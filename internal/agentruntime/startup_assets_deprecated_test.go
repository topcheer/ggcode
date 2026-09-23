package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

func TestBuildSkillsSystemPromptAnnotatesDeprecated(t *testing.T) {
	skills := []*commands.Command{
		{Name: "old-flow", Description: "Old workflow", Deprecated: true, ReplacedBy: "new-flow", Enabled: true},
		{Name: "new-flow", Description: "New workflow", Enabled: true},
	}
	out := BuildSkillsSystemPrompt(skills)
	if !strings.Contains(out, "(deprecated; successor: new-flow)") {
		t.Fatalf("expected deprecated annotation in prompt listing, got:\n%s", out)
	}
	if strings.Count(out, "(deprecated") != 1 {
		t.Fatalf("expected exactly one deprecated annotation, got:\n%s", out)
	}
}

func TestBuildSkillsSystemPromptNoAnnotationForActive(t *testing.T) {
	skills := []*commands.Command{
		{Name: "fresh", Description: "Fresh skill", Enabled: true},
	}
	out := BuildSkillsSystemPrompt(skills)
	if strings.Contains(out, "(deprecated") {
		t.Fatalf("active skill must not be annotated, got:\n%s", out)
	}
}
