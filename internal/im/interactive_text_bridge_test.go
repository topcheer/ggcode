package im

import (
	"context"
	"testing"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func TestInteractiveTextBridgeResolvesApproval(t *testing.T) {
	var resolvedID, resolvedDecision string
	bridge := &InteractiveTextBridge{
		Submit: func(context.Context, string) error {
			t.Fatal("expected approval reply not to submit normal text")
			return nil
		},
		CurrentApproval: func() (string, string, bool) {
			return "req-1", "bash", true
		},
		ResolveApproval: func(requestID, decision string) {
			resolvedID = requestID
			resolvedDecision = decision
		},
	}

	if err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{Text: "y"}); err != nil {
		t.Fatalf("SubmitInboundMessage returned error: %v", err)
	}
	if resolvedID != "req-1" || resolvedDecision != "allow" {
		t.Fatalf("unexpected approval resolution: id=%q decision=%q", resolvedID, resolvedDecision)
	}
}

func TestInteractiveTextBridgeResolvesAskUser(t *testing.T) {
	var resolvedID string
	var response toolpkg.AskUserResponse
	bridge := &InteractiveTextBridge{
		Submit: func(context.Context, string) error {
			t.Fatal("expected ask_user reply not to submit normal text")
			return nil
		},
		CurrentAskUser: func() (string, toolpkg.AskUserRequest, bool) {
			return "req-2", toolpkg.AskUserRequest{
				Title: "Question",
				Questions: []toolpkg.AskUserQuestion{{
					ID:      "q1",
					Title:   "Pick one",
					Kind:    toolpkg.AskUserKindSingle,
					Choices: []toolpkg.AskUserChoice{{ID: "a", Label: "A"}},
				}},
			}, true
		},
		ResolveAskUser: func(requestID string, resp toolpkg.AskUserResponse) {
			resolvedID = requestID
			response = resp
		},
	}

	if err := bridge.SubmitInboundMessage(context.Background(), InboundMessage{Text: "1"}); err != nil {
		t.Fatalf("SubmitInboundMessage returned error: %v", err)
	}
	if resolvedID != "req-2" {
		t.Fatalf("unexpected ask_user request id: %q", resolvedID)
	}
	if len(response.Answers) != 1 || len(response.Answers[0].SelectedChoiceIDs) != 1 || response.Answers[0].SelectedChoiceIDs[0] != "a" {
		t.Fatalf("unexpected ask_user response: %#v", response)
	}
}
