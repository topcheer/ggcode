package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// r382 wiring coverage: NewAgent must wire the run_command omitted-middle
// spill hook when the registry contains a RunCommand, must not panic on a
// nil registry (the 5a95aa5a2 hotfix), and must tolerate registries that
// lack run_command entirely. The nil case previously panicked inside
// Registry.Get (nil receiver deref) and broke
// TestMaybeInjectDynamicSystemPromptIncludesTemporal on CI.
func TestRunCommandSpillWiring(t *testing.T) {
	mp := &mockProvider{
		chatResp: &provider.ChatResponse{
			Message: provider.Message{
				Role:    "assistant",
				Content: []provider.ContentBlock{{Type: "text", Text: "ok"}},
			},
			Usage: provider.TokenUsage{InputTokens: 1, OutputTokens: 1},
		},
	}

	t.Run("wired when run_command present", func(t *testing.T) {
		registry := tool.NewRegistry()
		if err := registry.Register(&tool.RunCommand{}); err != nil {
			t.Fatalf("register run_command: %v", err)
		}
		a := NewAgent(mp, registry, "", 1)
		got, ok := registry.Get("run_command")
		if !ok {
			t.Fatal("run_command missing from registry")
		}
		rc, ok := got.(*tool.RunCommand)
		if !ok {
			t.Fatalf("run_command is %T, want *tool.RunCommand", got)
		}
		if rc.OmittedOutputSpiller == nil {
			t.Error("NewAgent did not wire OmittedOutputSpiller on run_command")
		}
		_ = a
	})

	t.Run("nil registry does not panic", func(t *testing.T) {
		a := NewAgent(mp, nil, "", 1)
		if a == nil {
			t.Fatal("NewAgent returned nil for nil registry")
		}
	})

	t.Run("registry without run_command does not panic", func(t *testing.T) {
		a := NewAgent(mp, tool.NewRegistry(), "", 1)
		if a == nil {
			t.Fatal("NewAgent returned nil")
		}
	})
}
