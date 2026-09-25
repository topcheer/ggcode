package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// r74: selective-escalation guidance (HiL-Bench arXiv:2604.09408) must be
// present in every interactive mode, not only autopilot, and only when the
// ask_user tool is registered for the surface.
func TestBuildInteractiveSystemPromptClarifications(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(tool.NewAskUserTool()); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}
	cfg := &config.Config{Language: "en"}
	wd := t.TempDir()

	prompt := BuildInteractiveSystemPrompt(cfg, wd, permission.AutoMode, reg, nil, nil, nil, "", "")
	checks := []struct{ name, contains string }{
		{"clarifications section", "## Clarifications"},
		{"ask once via ask_user", "ask once via `ask_user`"},
		{"non-interactive fallback", "do not retry it"},
		{"explicit assumption", "state that assumption explicitly"},
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c.contains) {
			t.Errorf("%s: prompt missing %q", c.name, c.contains)
		}
	}

	// Without ask_user registered (sub-agent/teammate surfaces) the section
	// must be omitted; those prompts carry their own no-ask_user constraints.
	reg2 := tool.NewRegistry()
	prompt2 := BuildInteractiveSystemPrompt(cfg, wd, permission.AutoMode, reg2, nil, nil, nil, "", "")
	if strings.Contains(prompt2, "## Clarifications") {
		t.Fatal("Clarifications section must be omitted when ask_user is not registered")
	}
}
