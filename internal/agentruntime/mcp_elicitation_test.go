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

// TestMCPElicitationRequiredEmptyDeclines pins #1678 case 2: a required
// field left EMPTY was silently dropped from the accept content (empty
// values are skipped) - the MCP server got an accept missing required
// fields with no error signal. An incomplete answer now declines.
func TestMCPElicitationRequiredEmptyDeclines(t *testing.T) {
	reg := toolpkg.NewRegistry()
	ask := toolpkg.NewAskUserTool()
	ask.SetHandler(func(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
		return toolpkg.AskUserResponse{
			Status: toolpkg.AskUserStatusSubmitted,
			Answers: []toolpkg.AskUserAnswer{
				{ID: "name", Answered: true, FreeformText: "alice"}, // filled
				{ID: "token", Answered: false},                      // required, left empty
			},
		}, nil
	})
	if err := reg.Register(ask); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}

	h := newMCPElicitationHandler(reg)
	res, err := h(context.Background(), mcp.ElicitationParams{
		Message: "Credentials?",
		Schema: mcp.ElicitationSchema{
			Required: []string{"name", "token"},
			Properties: map[string]mcp.ElicitationFieldSchema{
				"name":  {Type: "string"},
				"token": {Type: "string"},
			},
		},
	})
	if err != nil {
		t.Fatalf("elicitation failed: %v", err)
	}
	if res.Action != mcp.ElicitationActionDecline {
		t.Fatalf("action = %v, want decline (required token left empty)", res.Action)
	}
}

// TestMissingRequiredFieldsDirect: the helper distinguishes absent, empty-
// string, and filled values.
func TestMissingRequiredFieldsDirect(t *testing.T) {
	schema := mcp.ElicitationSchema{
		Required: []string{"a", "b", "c"},
		Properties: map[string]mcp.ElicitationFieldSchema{
			"a": {Type: "string"}, "b": {Type: "string"}, "c": {Type: "string"},
		},
	}
	// a filled, b absent, c empty string.
	content := map[string]any{"a": "x", "c": ""}
	missing := missingRequiredFields(schema, content)
	if len(missing) != 2 {
		t.Fatalf("want 2 missing (b, c), got %v", missing)
	}
	if len(missingRequiredFields(schema, map[string]any{"a": "1", "b": "2", "c": "3"})) != 0 {
		t.Fatal("fully-filled content must report nothing missing")
	}
}
