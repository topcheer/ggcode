package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// #4212: the r27 safe-default advisory was computed by checkAskQualityGate
// and then dropped (log only) - the model never saw the "proceed with the
// stated default" guidance. Wiring-level test: run a stream whose only tool
// call is an ask_user with a safe-default choice and assert the advisory
// text appears in the tool RESULT the next provider turn consumes.
func TestAskSafeDefaultAdvisoryWiredIntoResult(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{
			{
				Message: provider.Message{
					Role: "assistant",
					Content: []provider.ContentBlock{
						provider.ToolUseBlock("tool-1", "ask_user", json.RawMessage(`{
							"questions":[
								{
									"title":"Deploy now",
									"prompt":"Should I deploy to production now or later?",
									"kind":"single",
									"choices":[
										{"id":"deploy","label":"Deploy now"},
										{"id":"skip","label":"Skip (safe) - decide later"}
									]
								}
							]
						}`)),
					},
				},
			},
			{
				Message: provider.Message{
					Role:    "assistant",
					Content: []provider.ContentBlock{provider.TextBlock("Proceeding with the stated default.")},
				},
			},
		},
	}

	registry := tool.NewRegistry()
	askTool := tool.NewAskUserTool()
	askTool.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
		return tool.AskUserResponse{
			Status:        tool.AskUserStatusSubmitted,
			QuestionCount: len(req.Questions),
			AnsweredCount: 0,
		}, nil
	})
	if err := registry.Register(askTool); err != nil {
		t.Fatalf("register ask_user: %v", err)
	}

	a := NewAgent(mp, registry, "", 3)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.AutopilotMode))

	var gotResult string
	if err := a.RunStream(context.Background(), "deploy when ready", func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventToolResult && ev.Tool.Name == "ask_user" && !ev.IsError {
			gotResult = ev.Result
		}
	}); err != nil {
		t.Fatalf("RunStream failed: %v", err)
	}

	if gotResult == "" {
		t.Fatal("no successful ask_user tool result observed")
	}
	if !strings.Contains(strings.ToLower(gotResult), "default") {
		t.Fatalf("safe-default advisory missing from ask_user result (computed-then-dropped regression #4212): %q", gotResult)
	}
}
