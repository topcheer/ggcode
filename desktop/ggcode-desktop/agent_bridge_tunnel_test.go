package main

import (
	"testing"

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

	got := buildTunnelAskUserQuestions(req)
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

	resp := buildAskUserResponseFromTunnel(req, tool.AskUserStatusSubmitted, []tunnel.AskUserAnswer{
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
