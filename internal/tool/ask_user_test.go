package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAskUserToolRequiresInteractiveHandler(t *testing.T) {
	tool := NewAskUserTool()

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"questions":[{"title":"Need scope","prompt":"Pick scope","kind":"single","choices":[{"label":"small"}]}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected user-visible error when handler is missing")
	}
	if !strings.Contains(result.Content, "interactive TUI") {
		t.Fatalf("unexpected error content: %s", result.Content)
	}
}

func TestAskUserToolExecutesWithNormalizedRequest(t *testing.T) {
	tool := NewAskUserTool()
	tool.SetHandler(func(ctx context.Context, req AskUserRequest) (AskUserResponse, error) {
		if len(req.Questions) != 2 {
			t.Fatalf("expected 2 questions, got %d", len(req.Questions))
		}
		if req.Questions[0].ID == "" {
			t.Fatal("expected missing question id to be normalized")
		}
		if !req.Questions[0].AllowFreeform {
			t.Fatal("expected single choice question to allow freeform notes")
		}
		return AskUserResponse{
			Status: AskUserStatusSubmitted,
			Answers: []AskUserAnswer{
				{
					ID:                req.Questions[0].ID,
					Title:             req.Questions[0].Title,
					Kind:              req.Questions[0].Kind,
					CompletionStatus:  AskUserCompletionAnswered,
					AnswerMode:        AskUserAnswerModeSelectionOnly,
					Answered:          true,
					SelectedChoiceIDs: []string{"choice_1"},
					SelectedChoices:   []string{"frontend"},
				},
				{
					ID:               req.Questions[1].ID,
					Title:            req.Questions[1].Title,
					Kind:             req.Questions[1].Kind,
					CompletionStatus: AskUserCompletionAnswered,
					AnswerMode:       AskUserAnswerModeFreeformOnly,
					Answered:         true,
					FreeformText:     "Focus on release safety.",
				},
			},
		}, nil
	})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{
		"title":"Clarify rollout",
		"questions":[
			{"title":"Area","prompt":"Which area?","kind":"single","choices":[{"label":"frontend"}]},
			{"id":"notes","title":"Notes","prompt":"Anything else?","kind":"text"}
		]
	}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content)
	}

	var response AskUserResponse
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if response.Status != AskUserStatusSubmitted {
		t.Fatalf("expected submitted status, got %q", response.Status)
	}
	if response.QuestionCount != 2 {
		t.Fatalf("expected question_count=2, got %d", response.QuestionCount)
	}
	if response.AnsweredCount != 2 {
		t.Fatalf("expected answered_count=2, got %d", response.AnsweredCount)
	}
}
