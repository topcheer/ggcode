package acp

// Regression tests for GitHub issue #1140 (internal/acp).
//
// #1140 - the ACP ask_user handler closure only processed req.Questions[0].
// A batch of N>1 questions produced ONE permission round trip for the first
// question yet still returned status=submitted with QuestionCount=N and
// AnsweredCount=1, silently dropping questions 2..N behind a fake success
// signal. The TUI path (internal/tui) renders one tab per question, so
// multi-question batches are a product contract.
//
// Fix contract verified here: every question gets its own
// session/request_permission round trip carrying that question's OWN choice
// options, answers are aggregated in question order, AnsweredCount equals
// QuestionCount, a mid-batch dismissal fails loudly instead of faking
// success, and text-kind questions keep their pre-#1140 explicit fallback
// error (also fired when a text question appears mid-batch).

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

// issue1140Executor is the minimal surface the tests need from the registry
// entry (either the #1116 per-session dispatcher or the base singleton).
type issue1140Executor interface {
	Execute(ctx context.Context, input json.RawMessage) (tool.Result, error)
}

// issue1140Setup wires the full stack used by the pre-existing ask_user
// tests: a dual-pipe Transport, builtin tools, a Handler pumping messages,
// and an AgentLoop whose constructor installs the ask_user handler. Returns
// the executable registry entry plus the client-side pipe ends the test
// impersonates the IDE with.
func issue1140Setup(t *testing.T) (issue1140Executor, *io.PipeReader, *io.PipeWriter, context.CancelFunc) {
	t.Helper()

	cr, cw := io.Pipe() // client -> agent (responses)
	ar, aw := io.Pipe() // agent -> client (requests)

	agentTransport := NewTransport(cr, aw)

	registry := tool.NewRegistry()
	policy := permission.NewConfigPolicyWithMode(nil, nil, permission.AutoMode)
	if err := tool.RegisterBuiltinTools(registry, policy, "/tmp", nil); err != nil {
		t.Fatalf("register builtin tools: %v", err)
	}

	cfg := &config.Config{MaxIterations: 100}
	session := NewSession("/tmp", nil)
	handler := NewHandler(cfg, registry, agentTransport, nil)
	handler.initialized = true
	handler.sessions["test-session"] = session
	_ = NewAgentLoop(cfg, registry, agentTransport, session, ClientCapabilities{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go handler.Run(ctx)

	entry, ok := registry.Get("ask_user")
	if !ok {
		cancel()
		t.Fatal("ask_user not found in registry")
	}
	ex, ok := entry.(issue1140Executor)
	if !ok {
		cancel()
		t.Fatalf("registry entry %T lacks Execute", entry)
	}
	return ex, ar, cw, cancel
}

// issue1140Run executes an ask_user payload off-turn and surfaces either the
// decoded response or the tool-level error content.
func issue1140Run(t *testing.T, ex issue1140Executor, input string) (<-chan tool.AskUserResponse, <-chan string) {
	t.Helper()
	respCh := make(chan tool.AskUserResponse, 1)
	errCh := make(chan string, 1)
	go func() {
		result, err := ex.Execute(context.Background(), json.RawMessage(input))
		if err != nil {
			errCh <- err.Error()
			return
		}
		if result.IsError {
			errCh <- result.Content
			return
		}
		var resp tool.AskUserResponse
		if uerr := json.Unmarshal([]byte(result.Content), &resp); uerr != nil {
			errCh <- "unmarshal response: " + uerr.Error()
			return
		}
		respCh <- resp
	}()
	return respCh, errCh
}

// issue1140AnswerOne performs one IDE-side round trip: read the agent's
// permission request, optionally verify it carries the expected option ids,
// then answer with the given option id (or "" for a cancelled outcome).
func issue1140AnswerOne(t *testing.T, ar *io.PipeReader, cw *io.PipeWriter, wantOptionIDs []string, answerOptionID string) {
	t.Helper()

	req := readAgentRequest(t, ar)
	if req["method"] != "session/request_permission" {
		t.Fatalf("expected session/request_permission, got %v", req["method"])
	}
	params, _ := req["params"].(map[string]interface{})
	options, _ := params["options"].([]interface{})
	gotIDs := make([]string, 0, len(options))
	for _, o := range options {
		obj, _ := o.(map[string]interface{})
		id, _ := obj["optionId"].(string)
		gotIDs = append(gotIDs, id)
	}
	if wantOptionIDs != nil && strings.Join(gotIDs, ",") != strings.Join(wantOptionIDs, ",") {
		t.Fatalf("permission options = %v, want %v (#1140: options must come from THIS question)", gotIDs, wantOptionIDs)
	}

	outcome := "cancelled"
	if answerOptionID != "" {
		outcome = "selected"
	}
	writeAgentResponse(t, cw, req["id"], map[string]interface{}{
		"outcome": map[string]interface{}{
			"outcome":  outcome,
			"optionId": answerOptionID,
		},
	})
}

// TestIssue1140_AllQuestionsRoundTrippedAndAnswered drives a three-question
// batch (single + multi + single) and asserts N permission round trips, one
// aggregated answer per question in order, and AnsweredCount == QuestionCount.
// Pre-fix this test saw one round trip and got AnsweredCount=1.
func TestIssue1140_AllQuestionsRoundTrippedAndAnswered(t *testing.T) {
	ex, ar, cw, cancel := issue1140Setup(t)
	defer cancel()

	input := `{
		"title": "Deploy config",
		"questions": [
			{"id": "q_color", "title": "Color", "prompt": "Pick brand color", "kind": "single",
			 "choices": [{"id": "opt_red", "label": "Red"}, {"id": "opt_blue", "label": "Blue"}]},
			{"id": "q_top", "title": "Toppings", "prompt": "Pick toppings", "kind": "multi",
			 "choices": [{"id": "top_choc", "label": "Chocolate"}, {"id": "top_mint", "label": "Mint"}]},
			{"id": "q_flavor", "title": "Flavor", "prompt": "Pick flavor", "kind": "single",
			 "choices": [{"id": "fl_vanilla", "label": "Vanilla"}, {"id": "fl_cocoa", "label": "Cocoa"}]}
		]
	}`
	respCh, errCh := issue1140Run(t, ex, input)

	// IDE answers the three successive modals, each showing that
	// question's own options.
	issue1140AnswerOne(t, ar, cw, []string{"opt_red", "opt_blue"}, "opt_blue")
	issue1140AnswerOne(t, ar, cw, []string{"top_choc", "top_mint"}, "top_mint")
	issue1140AnswerOne(t, ar, cw, []string{"fl_vanilla", "fl_cocoa"}, "fl_vanilla")

	select {
	case resp := <-respCh:
		if resp.Status != tool.AskUserStatusSubmitted {
			t.Errorf("status = %q, want %q", resp.Status, tool.AskUserStatusSubmitted)
		}
		if resp.QuestionCount != 3 {
			t.Errorf("QuestionCount = %d, want 3", resp.QuestionCount)
		}
		if resp.AnsweredCount != 3 {
			t.Errorf("AnsweredCount = %d, want 3 (#1140: every question must be answered)", resp.AnsweredCount)
		}
		if len(resp.Answers) != 3 {
			t.Fatalf("Answers = %d, want 3", len(resp.Answers))
		}
		wantOrder := []struct {
			id    string
			comps string
		}{{"q_color", "opt_blue"}, {"q_top", "top_mint"}, {"q_flavor", "fl_vanilla"}}
		for i, want := range wantOrder {
			ans := resp.Answers[i]
			if ans.ID != want.id {
				t.Errorf("Answers[%d].ID = %q, want %q (order must follow batch order)", i, ans.ID, want.id)
			}
			if !ans.Answered {
				t.Errorf("Answers[%d] (%s) not marked answered", i, ans.ID)
			}
			if ans.CompletionStatus != tool.AskUserCompletionAnswered {
				t.Errorf("Answers[%d].CompletionStatus = %q, want %q", i, ans.CompletionStatus, tool.AskUserCompletionAnswered)
			}
			if len(ans.SelectedChoiceIDs) != 1 || ans.SelectedChoiceIDs[0] != want.comps {
				t.Errorf("Answers[%d].SelectedChoiceIDs = %v, want [%s]", i, ans.SelectedChoiceIDs, want.comps)
			}
			if len(ans.SelectedChoices) != 1 || ans.SelectedChoices[0] != want.comps {
				t.Errorf("Answers[%d].SelectedChoices = %v, want [%s]", i, ans.SelectedChoices, want.comps)
			}
		}
	case errMsg := <-errCh:
		t.Fatalf("multi-question batch failed: %s", errMsg)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for aggregated response")
	}
}

// TestIssue1140_CancelMidBatchFailsLoudly verifies a dismissal on question 2
// of 3 surfaces an error instead of the old silent fake success
// (status=submitted with questions dropped).
func TestIssue1140_CancelMidBatchFailsLoudly(t *testing.T) {
	ex, ar, cw, cancel := issue1140Setup(t)
	defer cancel()

	input := `{
		"questions": [
			{"id": "q1", "title": "First", "prompt": "First?", "kind": "single",
			 "choices": [{"id": "a1", "label": "A"}, {"id": "b1", "label": "B"}]},
			{"id": "q2", "title": "Second", "prompt": "Second?", "kind": "single",
			 "choices": [{"id": "a2", "label": "A"}, {"id": "b2", "label": "B"}]},
			{"id": "q3", "title": "Third", "prompt": "Third?", "kind": "single",
			 "choices": [{"id": "a3", "label": "A"}, {"id": "b3", "label": "B"}]}
		]
	}`
	respCh, errCh := issue1140Run(t, ex, input)

	issue1140AnswerOne(t, ar, cw, []string{"a1", "b1"}, "a1")
	issue1140AnswerOne(t, ar, cw, []string{"a2", "b2"}, "") // user dismisses Q2

	select {
	case errMsg := <-errCh:
		if !strings.Contains(errMsg, "dismissed") {
			t.Errorf("expected dismissal error, got: %s", errMsg)
		}
	case resp := <-respCh:
		t.Fatalf("mid-batch cancel must not fake success, got status=%s answered=%d/%d",
			resp.Status, resp.AnsweredCount, resp.QuestionCount)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for dismissal error")
	}
}

// TestIssue1140_TextMidBatchGetsExplicitFallback keeps the pre-#1140
// guarantee for text questions (explicit IsError telling the LLM to fall back
// to plain text) even when the text question trails answered choice
// questions - never a silent drop.
func TestIssue1140_TextMidBatchGetsExplicitFallback(t *testing.T) {
	ex, ar, cw, cancel := issue1140Setup(t)
	defer cancel()

	input := `{
		"questions": [
			{"id": "q_choice", "title": "Env", "prompt": "Which env?", "kind": "single",
			 "choices": [{"id": "env_dev", "label": "Dev"}, {"id": "env_prod", "label": "Prod"}]},
			{"id": "q_free", "title": "Notes", "prompt": "Any notes?", "kind": "text"}
		]
	}`
	respCh, errCh := issue1140Run(t, ex, input)

	issue1140AnswerOne(t, ar, cw, []string{"env_dev", "env_prod"}, "env_prod")
	// Text question presents Submit/Cancel; user submits, handler must then
	// emit the explicit fallback error naming the question.
	req := readAgentRequest(t, ar)
	params, _ := req["params"].(map[string]interface{})
	options, _ := params["options"].([]interface{})
	foundSubmit := false
	for _, o := range options {
		obj, _ := o.(map[string]interface{})
		if id, _ := obj["optionId"].(string); id == "submit" {
			foundSubmit = true
		}
	}
	if !foundSubmit {
		t.Errorf("text question should still present Submit option, got %v", options)
	}
	writeAgentResponse(t, cw, req["id"], map[string]interface{}{
		"outcome": map[string]interface{}{"outcome": "selected", "optionId": "submit"},
	})

	select {
	case errMsg := <-errCh:
		if !strings.Contains(errMsg, "does not support text input") {
			t.Errorf("expected text-input fallback error, got: %s", errMsg)
		}
		if !strings.Contains(errMsg, "q_free") {
			t.Errorf("fallback error should name offending question id q_free, got: %s", errMsg)
		}
	case resp := <-respCh:
		t.Fatalf("text question must fail explicitly, got status=%s answered=%d/%d",
			resp.Status, resp.AnsweredCount, resp.QuestionCount)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for text fallback error")
	}
}
