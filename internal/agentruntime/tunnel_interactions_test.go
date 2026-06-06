package agentruntime

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestBuildTunnelAskUserQuestionsPreservesPromptMetadata(t *testing.T) {
	req := tool.AskUserRequest{
		Title: "Clarify rollout",
		Questions: []tool.AskUserQuestion{
			{
				ID:            "scope",
				Title:         "Scope",
				Prompt:        "Which scope should we use?",
				Kind:          tool.AskUserKindSingle,
				AllowFreeform: true,
				Placeholder:   "Optional notes",
				Choices:       []tool.AskUserChoice{{ID: "small", Label: "Small"}},
			},
		},
	}

	got := BuildTunnelAskUserQuestions(req)
	if len(got) != 1 {
		t.Fatalf("expected 1 question, got %d", len(got))
	}
	if got[0].Prompt != "Which scope should we use?" {
		t.Fatalf("expected prompt to be preserved, got %q", got[0].Prompt)
	}
	if !got[0].AllowFreeform {
		t.Fatal("expected allow_freeform to be preserved")
	}
	if got[0].Placeholder != "Optional notes" {
		t.Fatalf("expected placeholder to be preserved, got %q", got[0].Placeholder)
	}
}

func TestBuildAskUserResponseFromTunnelBuildsStructuredAnswers(t *testing.T) {
	req := tool.AskUserRequest{
		Title: "Clarify rollout",
		Questions: []tool.AskUserQuestion{
			{
				ID:      "scope",
				Title:   "Scope",
				Prompt:  "Which scope should we use?",
				Kind:    tool.AskUserKindSingle,
				Choices: []tool.AskUserChoice{{ID: "small", Label: "Small"}},
			},
			{
				ID:     "notes",
				Title:  "Notes",
				Prompt: "Anything else?",
				Kind:   tool.AskUserKindText,
			},
		},
	}

	resp := BuildAskUserResponseFromTunnel(req, tool.AskUserStatusSubmitted, []tunnel.AskUserAnswer{
		{QuestionID: "scope", ChoiceIDs: []string{"small"}},
		{QuestionID: "notes", FreeformText: "Ship tonight"},
	})

	if resp.Status != tool.AskUserStatusSubmitted {
		t.Fatalf("expected submitted status, got %q", resp.Status)
	}
	if resp.AnsweredCount != 2 {
		t.Fatalf("expected answered_count=2, got %d", resp.AnsweredCount)
	}
	if resp.Answers[0].SelectedChoices[0] != "Small" {
		t.Fatalf("expected selected choice label, got %+v", resp.Answers[0].SelectedChoices)
	}
	if !resp.Answers[1].Answered {
		t.Fatal("expected text answer to be marked answered")
	}
}

func TestBuildTunnelAskUserAnswersCopiesSelections(t *testing.T) {
	resp := tool.AskUserResponse{
		Status: tool.AskUserStatusSubmitted,
		Answers: []tool.AskUserAnswer{
			{
				ID:                "scope",
				SelectedChoiceIDs: []string{"small"},
				FreeformText:      "notes",
			},
		},
	}

	got := BuildTunnelAskUserAnswers(resp)
	if len(got) != 1 || len(got[0].ChoiceIDs) != 1 || got[0].ChoiceIDs[0] != "small" {
		t.Fatalf("unexpected answers: %+v", got)
	}
	resp.Answers[0].SelectedChoiceIDs[0] = "large"
	if got[0].ChoiceIDs[0] != "small" {
		t.Fatalf("expected copied choice ids, got %+v", got[0].ChoiceIDs)
	}
}

func TestApprovalDecisionFromTunnel(t *testing.T) {
	tests := []struct {
		decision string
		want     permission.Decision
	}{
		{decision: tunnel.DecisionAllow, want: permission.Allow},
		{decision: tunnel.DecisionAlwaysAllow, want: permission.Allow},
		{decision: "always", want: permission.Allow},
		{decision: tunnel.DecisionDeny, want: permission.Deny},
		{decision: "", want: permission.Deny},
	}

	for _, tc := range tests {
		if got := ApprovalDecisionFromTunnel(tc.decision); got != tc.want {
			t.Fatalf("decision %q: expected %q, got %q", tc.decision, tc.want, got)
		}
	}
}
