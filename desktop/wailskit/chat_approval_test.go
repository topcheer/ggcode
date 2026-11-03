package wailskit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	agentruntime "github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func TestMobileAlwaysAllowGrantsCommandPatternNotWholeTool(t *testing.T) {
	broker := agentruntime.NewInteractionBroker()
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	a := agent.NewAgent(nil, tool.NewRegistry(), "", 5)
	a.SetPermissionPolicy(policy)
	bridge := &ChatBridge{interactions: broker, agent: a}

	approved := make(chan permission.Decision, 1)
	go func() {
		approved <- broker.AwaitApproval(context.Background(), agentruntime.ApprovalRequest{
			ID:       "req-1038",
			ToolName: "run_command",

			Input: `{"command":"git push origin main"}`,
		})
	}()

	waitForApprovalPending(t, broker, "req-1038")
	bridge.HandleMobileApprovalResponse(tunnel.ApprovalResponseData{ID: "req-1038", Decision: "always_allow"})

	select {
	case d := <-approved:
		if d != permission.Allow {
			t.Fatalf("waiter should resolve to Allow, got %v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval waiter never resolved")
	}

	gitInput, _ := json.Marshal(map[string]string{"command": "git push origin main"})
	if d, err := policy.Check("run_command", gitInput); err != nil || d != permission.Allow {
		t.Fatalf("git push should be allowed by the granted pattern, got %v (err=%v)", d, err)
	}

	edInput, _ := json.Marshal(map[string]string{"command": "rm -rf /"})
	if d, err := policy.Check("run_command", edInput); err != nil {
		t.Fatalf("check error: %v", err)
	} else if d != permission.Ask {
		t.Fatalf("always_allow on 'git push' must not disarm the extremely-dangerous floor (#1038 regression), got %v", d)
	}
}

func TestMobileAlwaysAllowNonCommandToolFallsBackToOverride(t *testing.T) {
	broker := agentruntime.NewInteractionBroker()
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	a := agent.NewAgent(nil, tool.NewRegistry(), "", 5)
	a.SetPermissionPolicy(policy)
	bridge := &ChatBridge{interactions: broker, agent: a}

	approved := make(chan permission.Decision, 1)
	go func() {
		approved <- broker.AwaitApproval(context.Background(), agentruntime.ApprovalRequest{
			ID:       "req-1038b",
			ToolName: "write_file",

			Input: `{"path":"notes.txt"}`,
		})
	}()

	waitForApprovalPending(t, broker, "req-1038b")
	bridge.HandleMobileApprovalResponse(tunnel.ApprovalResponseData{ID: "req-1038b", Decision: "always_allow"})

	select {
	case <-approved:
	case <-time.After(2 * time.Second):
		t.Fatal("approval waiter never resolved")
	}

	input, _ := json.Marshal(map[string]string{"path": "notes.txt"})
	if d, err := policy.Check("write_file", input); err != nil || d != permission.Allow {
		t.Fatalf("non-command tool should fall back to tool-level Allow, got %v (err=%v)", d, err)
	}
}

func TestRequestAskUserSurvivesParentCancel(t *testing.T) {
	broker := agentruntime.NewInteractionBroker()
	bridge := &ChatBridge{interactions: broker}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := tool.AskUserRequest{
		Title: "pick one",
		Questions: []tool.AskUserQuestion{{
			ID:      "q1",
			Title:   "choice",
			Kind:    "single",
			Choices: []tool.AskUserChoice{{ID: "a", Label: "A"}},
		}},
	}

	type result struct {
		resp tool.AskUserResponse
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := bridge.RequestAskUser(ctx, "ask-1039", req)
		res <- result{resp, err}
	}()

	time.Sleep(100 * time.Millisecond)
	select {
	case r := <-res:
		t.Fatalf("RequestAskUser returned early (err=%v) - parent cancel must not abort the wait", r.err)
	default:
	}

	bridge.RespondAskUser("ask-1039", tool.AskUserResponse{
		Status:        "answered",
		Title:         req.Title,
		QuestionCount: 1,
		AnsweredCount: 1,
		Answers: []tool.AskUserAnswer{{
			ID:                "q1",
			Title:             "choice",
			Kind:              "single",
			CompletionStatus:  "answered",
			Answered:          true,
			SelectedChoiceIDs: []string{"a"},
		}},
	})

	select {
	case r := <-res:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.resp.Status != "answered" {
			t.Fatalf("expected answered response, got %+v", r.resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RequestAskUser never resolved after RespondAskUser")
	}
}

func waitForApprovalPending(t *testing.T, broker *agentruntime.InteractionBroker, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := broker.PendingApproval(id); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval %s never became pending", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
