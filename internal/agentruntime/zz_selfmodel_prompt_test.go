package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

// TestSubAgentPromptCarriesSelfModelCard pins the delegation-injection wiring
// (#feat-self-model): BuildSubAgentSystemPrompt / BuildTeammateSystemPrompt
// load the workspace self-model and embed the DelegationContext card.
func TestSubAgentPromptCarriesSelfModelCard(t *testing.T) {
	dir := t.TempDir()
	m := agent.LoadSelfModel(agent.SelfModelPath(dir))
	m.Record(agent.SelfModelPath(dir), "grep", true, "")
	m.Record(agent.SelfModelPath(dir), "grep", true, "")
	m.Record(agent.SelfModelPath(dir), "grep", true, "")
	const marker = "zzmarker-pivot-on-refactor"
	m.AddLimit(agent.SelfModelPath(dir), marker)

	ctx := SubAgentPromptContext{WorkingDir: dir}
	got := BuildSubAgentSystemPrompt(ctx, "do the thing", "")
	if !strings.Contains(got, "[agent self-model]") {
		t.Fatalf("sub-agent prompt must embed self-model card, got tail %q", tailFor(got))
	}
	if !strings.Contains(got, marker) {
		t.Fatal("sub-agent prompt must carry declared-limit text")
	}
	if !strings.Contains(got, "grep 100%") {
		t.Fatal("sub-agent prompt must carry strength stats")
	}

	team := BuildTeammateSystemPrompt(ctx, "worker", "team-1")
	if !strings.Contains(team, "[agent self-model]") || !strings.Contains(team, marker) {
		t.Fatal("teammate prompt must embed self-model card with limits")
	}
}

// TestSubAgentPromptNoCardWhenEmpty pins zero-signal behavior: no card
// section when the workspace has no self-model data.
func TestSubAgentPromptNoCardWhenEmpty(t *testing.T) {
	ctx := SubAgentPromptContext{WorkingDir: t.TempDir()}
	got := BuildSubAgentSystemPrompt(ctx, "task", "")
	if strings.Contains(got, "[agent self-model]") {
		t.Fatal("empty self-model must not inject a card")
	}
}

func tailFor(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	_ = filepath.Separator
	_ = os.Getenv
	return s
}
