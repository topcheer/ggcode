package agentruntime

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/mcp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// TestMCPURLElicitationConsentFlow covers the MCP 2025-11-25 URL mode:
// consent routes through ask_user as an explicit prompt that displays the
// target host, the accept response carries no content (the interaction is
// out-of-band), and cancel/decline map onto the spec's three-action model.
func TestMCPURLElicitationConsentFlow(t *testing.T) {
	cases := []struct {
		name       string
		resp       toolpkg.AskUserResponse
		respErr    error
		wantAction mcp.ElicitationAction
		wantErr    bool
	}{
		{
			name: "consent accepts with no content",
			resp: toolpkg.AskUserResponse{
				Status: toolpkg.AskUserStatusSubmitted,
				Answers: []toolpkg.AskUserAnswer{{
					ID: "consent", Answered: true, SelectedChoiceIDs: []string{"accept"},
				}},
			},
			wantAction: mcp.ElicitationActionAccept,
		},
		{
			name: "explicit decline",
			resp: toolpkg.AskUserResponse{
				Status: toolpkg.AskUserStatusSubmitted,
				Answers: []toolpkg.AskUserAnswer{{
					ID: "consent", Answered: true, SelectedChoiceIDs: []string{"decline"},
				}},
			},
			wantAction: mcp.ElicitationActionDecline,
		},
		{
			name:       "cancel maps to cancel",
			resp:       toolpkg.AskUserResponse{Status: toolpkg.AskUserStatusCancelled},
			wantAction: mcp.ElicitationActionCancel,
		},
		{
			name:    "ask surface failure surfaces error",
			respErr: context.DeadlineExceeded,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := toolpkg.NewRegistry()
			ask := toolpkg.NewAskUserTool()
			var captured toolpkg.AskUserRequest
			ask.SetHandler(func(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
				captured = req
				if tc.respErr != nil {
					return toolpkg.AskUserResponse{}, tc.respErr
				}
				return tc.resp, nil
			})
			if err := reg.Register(ask); err != nil {
				t.Fatalf("register ask_user: %v", err)
			}
			h := newMCPElicitationHandler(reg)
			res, err := h(context.Background(), mcp.ElicitationParams{
				Mode:          mcp.ElicitationModeURL,
				ElicitationID: "550e8400-e29b-41d4-a716-446655440000",
				URL:           "https://mcp.example.com/ui/set_api_key",
				Message:       "Please provide your API key to continue.",
			})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error from interrupted ask surface")
				}
				return
			}
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}
			if res.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", res.Action, tc.wantAction)
			}
			if len(res.Content) != 0 {
				t.Fatalf("url mode response must omit content, got %v", res.Content)
			}
			// The consent prompt must surface the target host for review
			// (spec: clearly display the target domain/host).
			if len(captured.Questions) != 1 {
				t.Fatalf("want 1 consent question, got %d", len(captured.Questions))
			}
			if !strings.Contains(captured.Questions[0].Prompt, "mcp.example.com") {
				t.Fatalf("consent prompt must display the target host, got %q", captured.Questions[0].Prompt)
			}
		})
	}
}
