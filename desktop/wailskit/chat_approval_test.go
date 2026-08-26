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

// TestMobileAlwaysAllowGrantsCommandPatternNotWholeTool guards #1038: the
// mobile always_allow response must grant a fine-grained command pattern
// (like desktop RespondApproval / TUI / IM paths), NOT a blanket
// SetOverride(tool, Allow) that auto-approves every future invocation of the
// tool.
// TestMobileAlwaysAllowGrantsCommandPatternNotWholeTool guards #1038: the
// mobile always_allow response must grant a fine-grained command pattern
// (like desktop RespondApproval / TUI / IM paths), NOT a blanket
// SetOverride(tool, Allow) that auto-approves every future invocation of the
// tool. SupervisedMode is the discriminating scenario: commands default to
// Ask, so a blanket override would silently auto-approve everything, while
// a pattern grant only covers the approved prefix.
func TestMobileAlwaysAllowGrantsCommandPatternNotWholeTool(t *testing.T) {
	broker := agentruntime.NewInteractionBroker()
	policy := permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.SupervisedMode)
	a := agent.NewAgent(nil, tool.NewRegistry(), "", 5)
	a.SetPermissionPolicy(policy)
	bridge := &ChatBridge{interactions: broker, agent: a}

	// Register a pending run_command approval for "git push origin main".
	approved := make(chan permission.Decision, 1)
	go func() {
		approved <- broker.AwaitApproval(context.Background(), agentruntime.ApprovalRequest{
			ID:       "req-1038",
			ToolName: "run_command",
			// Input is the tool-argument JSON (as in production) -
			// ExtractCommandFromInput parses the "command" key from it.
			Input: `{"command":"git push origin main"}`,
		})
	}()

	// Wait for the waiter to be registered, then answer via the mobile path.
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

	// The granted pattern must cover the approved command...
	gitInput, _ := json.Marshal(map[string]string{"command": "git push origin main"})
	if d, err := policy.Check("run_command", gitInput); err != nil || d != permission.Allow {
		t.Fatalf("git push should be allowed by the granted pattern, got %v (err=%v)", d, err)
	}
	// ...but NOT blanket-allow the whole tool: a different, unrelated command
	// must still require approval (old SetOverride behavior allowed it).
	otherInput, _ := json.Marshal(map[string]string{"command": "docker system prune -af"})
	if d, err := policy.Check("run_command", otherInput); err != nil {
		t.Fatalf("check error: %v", err)
	} else if d == permission.Allow {
		t.Fatal("always_allow on 'git push' must not blanket-allow unrelated commands like 'docker system prune' (#1038 regression)")
	}
}

// TestMobileAlwaysAllowNonCommandToolFallsBackToOverride locks the fallback
// branch of #1038: tools without an extractable command keep the tool-level
// override behavior.
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
			// No command/input key: ExtractCommandFromInput returns "" so the
			// fallback SetOverride branch is exercised.
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

// TestRequestAskUserSurvivesParentCancel guards #1039: the wait is wrapped in
// WithoutCancel (bounded by a 15min timeout), so cancelling the parent
// context must NOT abort a pending ask_user - only an actual response (or
// the timeout) ends the wait. A plain ctx would have returned
// context.Canceled immediately.
func TestRequestAskUserSurvivesParentCancel(t *testing.T) {
	broker := agentruntime.NewInteractionBroker()
	bridge := &ChatBridge{interactions: broker}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // parent already dead before the request

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

	// Give the waiter a moment to register; if the parent cancel leaked
	// through, the call would already have returned ctx.Err().
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

// waitForApprovalPending polls the broker until AwaitApproval has registered
// the waiter (or fails the test after a short deadline).
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
