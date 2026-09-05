package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestMCPElicitationRoutesThroughAskUserTool(t *testing.T) {
	// Regression for #1484: the elicitation handler depended on a
	// package-global InteractionBroker whose setter had zero call sites, so
	// every elicitation/create died at "no interaction broker available".
	// The fix routes through the session's registered ask_user tool, reusing
	// the per-surface handler that TUI/daemon/desktop install at startup.
	reg := toolpkg.NewRegistry()
	ask := toolpkg.NewAskUserTool()
	ask.SetHandler(func(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
		return toolpkg.AskUserResponse{
			Status: toolpkg.AskUserStatusSubmitted,
			Answers: []toolpkg.AskUserAnswer{{
				ID:           "name",
				Answered:     true,
				FreeformText: "alice",
			}},
		}, nil
	})
	if err := reg.Register(ask); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}

	h := newMCPElicitationHandler(reg)
	res, err := h(context.Background(), mcp.ElicitationParams{
		Message: "Who should receive the deploy?",
		Schema: mcp.ElicitationSchema{
			Properties: map[string]mcp.ElicitationFieldSchema{
				"name": {Type: "string"},
			},
		},
	})
	if err != nil {
		t.Fatalf("elicitation failed: %v", err)
	}
	if res.Action != mcp.ElicitationActionAccept {
		t.Fatalf("action = %v, want accept", res.Action)
	}
	if got, _ := res.Content["name"].(string); got != "alice" {
		t.Fatalf("content[name] = %v, want alice", res.Content["name"])
	}
}

func TestMCPElicitationNoHandlerErrors(t *testing.T) {
	// Non-interactive sessions (no surface handler installed) must get a
	// clear error instead of a hang.
	reg := toolpkg.NewRegistry()
	if err := reg.Register(toolpkg.NewAskUserTool()); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}
	h := newMCPElicitationHandler(reg)
	_, err := h(context.Background(), mcp.ElicitationParams{
		Message: "hi",
		Schema: mcp.ElicitationSchema{
			Properties: map[string]mcp.ElicitationFieldSchema{
				"name": {Type: "string"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "handler not available") {
		t.Fatalf("want handler-not-available error, got: %v", err)
	}
}
