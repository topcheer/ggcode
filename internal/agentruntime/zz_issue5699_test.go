package agentruntime

import (
	"context"
	"errors"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// #5699: Esc on an MCP input_required round must defer (queue a pending
// answer) instead of cancelling, but only while the run context is alive.

func new5699Registry(t *testing.T, status string) *toolpkg.Registry {
	t.Helper()
	reg := toolpkg.NewRegistry()
	ask := toolpkg.NewAskUserTool()
	ask.SetHandler(func(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
		return toolpkg.AskUserResponse{Status: status}, nil
	})
	if err := reg.Register(ask); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}
	return reg
}

func TestIssue5699EscDefersElicitation(t *testing.T) {
	h := newMCPElicitationHandler(new5699Registry(t, toolpkg.AskUserStatusCancelled))
	_, err := h(context.Background(), mcp.ElicitationParams{
		Message: "Which city?",
		Schema: mcp.ElicitationSchema{
			Properties: map[string]mcp.ElicitationFieldSchema{
				"city": {Type: "string"},
			},
		},
	})
	if !errors.Is(err, mcp.ErrDeferInputRequest) {
		t.Fatalf("Esc must return the defer sentinel, got: %v", err)
	}
}

func TestIssue5699RunCancelKeepsCancelAction(t *testing.T) {
	h := newMCPElicitationHandler(new5699Registry(t, toolpkg.AskUserStatusCancelled))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // run is being torn down: Esc-like cancel must stay a cancel
	res, err := h(ctx, mcp.ElicitationParams{
		Message: "Which city?",
		Schema: mcp.ElicitationSchema{
			Properties: map[string]mcp.ElicitationFieldSchema{
				"city": {Type: "string"},
			},
		},
	})
	if err != nil {
		t.Fatalf("cancelled ctx must not defer: %v", err)
	}
	if res == nil || res.Action != mcp.ElicitationActionCancel {
		t.Fatalf("cancelled ctx must keep cancel action, got: %+v", res)
	}
}
