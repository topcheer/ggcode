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
	// r74: the no-handler fallback must be a directed escalation fallback
	// (HiL-Bench arXiv:2604.09408) instead of a bare surface claim.
	for _, want := range []string{"no interactive user surface", "Do not retry", "safest reversible assumption", "state the assumption explicitly"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("fallback content should mention %q, got %q", want, result.Content)
		}
	}
}

func TestAskUserAskDirectRequiresHandler(t *testing.T) {
	tool := NewAskUserTool()
	_, err := tool.AskDirect(context.Background(), AskUserRequest{
		Questions: []AskUserQuestion{{Title: "Scope", Prompt: "Pick scope", Kind: AskUserKindSingle, Choices: []AskUserChoice{{Label: "small"}}}},
	})
	if err == nil {
		t.Fatal("expected error when no surface handler is installed")
	}
	if !strings.Contains(err.Error(), "no interactive user surface") {
		t.Fatalf("unexpected AskDirect error: %v", err)
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
		if req.Questions[0].AllowFreeform {
			t.Log("note: AllowFreeform=true here means the payload set it or used a *_with_freeform alias (#804 default false, #1677-4b alias sets it)")
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

func TestAskUserToolDescriptionDiscouragesUnnecessaryQuestions(t *testing.T) {
	tool := NewAskUserTool()
	desc := tool.Description()
	for _, want := range []string{"material clarification", "answer changes what you do next", "no safe best guess"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("ask_user description should mention %q, got %q", want, desc)
		}
	}
	params := string(tool.Parameters())
	if !strings.Contains(params, "only ask material clarifications") {
		t.Fatalf("ask_user schema should discourage unnecessary questions, got %s", params)
	}
}
