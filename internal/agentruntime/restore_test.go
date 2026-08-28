package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

// TestRestoreSessionClearsRunAdded verifies that RestoreSessionIntoAgent
// does not leave restored messages in runAdded. Without this, the first
// agent run after restore would re-persist all messages to the JSONL file,
// doubling it on every restart.
func TestRestoreSessionClearsRunAdded(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "test system prompt", 10)

	ses := &session.Session{
		ID: "test-session",
		ContextMessages: []provider.Message{
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
			{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hi there"}}},
			{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "what is 1+1?"}}},
		},
	}

	RestoreSessionIntoAgent(ag, ses)

	// After restore, runAdded must be empty — these messages already exist
	// in the JSONL file and must not be re-persisted.
	runAdded := ag.AddedSinceRunStart()
	if len(runAdded) != 0 {
		t.Fatalf("runAdded should be empty after restore, got %d messages (first: role=%s)", len(runAdded), runAdded[0].Role)
	}
}

// TestRestoreSessionMessagesLoaded verifies that restored messages are
// accessible via the agent's context manager for LLM calls.
func TestRestoreSessionMessagesLoaded(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "world"}}},
	}

	ses := &session.Session{
		ID:              "test-session",
		ContextMessages: msgs,
	}

	RestoreSessionIntoAgent(ag, ses)

	// Messages should be loaded into the context manager.
	// runAdded cleared, but messages should still be in the context.
	// Add a new message and verify it's the only one in runAdded.
	ag.AddMessage(provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: "new message"}},
	})

	runAdded := ag.AddedSinceRunStart()
	if len(runAdded) != 1 {
		t.Fatalf("expected 1 message in runAdded after post-restore Add, got %d", len(runAdded))
	}
	if runAdded[0].Role != "user" {
		t.Fatalf("expected user role, got %s", runAdded[0].Role)
	}
}

// TestRestoreSessionWithCheckpoint verifies that ALL ContextMessages
// (including the summary) are loaded into the agent context, and the
// checkpoint baseline is set correctly.
//
// This test guards against a regression where CheckpointMessageCount
// was used to slice ContextMessages, accidentally skipping ALL messages
// because CheckpointMessageCount == len(ContextMessages).
func TestRestoreSessionWithCheckpoint(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

	// Simulate the real structure produced by loadSession:
	// ContextMessages = [summary_system_msg, ...post-checkpoint user/assistant msgs]
	cpMsgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "[Previous conversation summary]\nsystem prompt"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "checkpoint user msg"}}},
	}
	postMsgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "post checkpoint"}}},
	}
	allMsgs := append(append([]provider.Message{}, cpMsgs...), postMsgs...)

	ses := &session.Session{
		ID:                     "test-cp-session",
		ContextMessages:        allMsgs,
		CheckpointTokens:       50000,
		CheckpointMessageCount: len(allMsgs), // loadSession sets this to len(ContextMessages)
	}

	RestoreSessionIntoAgent(ag, ses)

	// runAdded must still be empty.
	if runAdded := ag.AddedSinceRunStart(); len(runAdded) != 0 {
		t.Fatalf("runAdded should be empty after checkpoint restore, got %d", len(runAdded))
	}

	// ALL ContextMessages must be loaded into the agent — not just the
	// post-checkpoint slice. CheckpointMessageCount must NOT cause slicing
	// of ContextMessages (it was already built checkpoint-aware by loadSession).
	loadedMsgs := ag.Messages()
	// The agent prepends its own system prompt, so we expect system + allMsgs.
	// Verify all non-system messages from the session are present.
	expectedNonSystem := 0
	for _, m := range allMsgs {
		if m.Role != "system" {
			expectedNonSystem++
		}
	}
	actualNonSystem := 0
	for _, m := range loadedMsgs {
		if m.Role != "system" {
			actualNonSystem++
		}
	}
	if actualNonSystem != expectedNonSystem {
		t.Fatalf("expected %d non-system messages loaded, got %d (loaded %d total msgs)",
			expectedNonSystem, actualNonSystem, len(loadedMsgs))
	}

	// Token count should reflect checkpoint baseline.
	tokens := ag.ContextManager().TokenCount()
	if tokens < 50000 {
		t.Fatalf("token count should be >= checkpoint baseline (50000), got %d", tokens)
	}
}

// TestRestoreNilAgent verifies no panic on nil inputs.
func TestRestoreNilAgent(t *testing.T) {
	// Should not panic.
	_, _, _ = RestoreSessionIntoAgent(nil, &session.Session{ID: "test"})
	_, _, _ = RestoreSessionIntoAgent(&agent.Agent{}, nil)
}

// TestRestoreSessionStaleUsageBaselineClamped pins the under-direction
// stale-baseline clamp: when the last recorded usage grossly under-reports
// the restored context (e.g. the session was last saved by a build whose
// reload produced a few-K stub), the restore must fall back to the
// calibrated estimate of the restored messages instead of the stale
// measurement -- otherwise the TUI keeps showing few-K context usage after
// the context itself was fixed.
func TestRestoreSessionStaleUsageBaselineClamped(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

	// Large restored context: 60 messages x ~500 ASCII chars ≈ 30K chars →
	// local estimate in the several-thousand-token range.
	msgs := make([]provider.Message, 60)
	for i := range msgs {
		text := strings.Repeat("a", 500)
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = provider.Message{
			Role:    role,
			Content: []provider.ContentBlock{{Type: "text", Text: text}},
		}
	}

	ses := &session.Session{
		ID:              "stale-usage-session",
		ContextMessages: msgs,
		UsageHistory: []session.UsageEntry{{
			Usage: provider.TokenUsage{InputTokens: 2000, OutputTokens: 300},
		}},
	}

	RestoreSessionIntoAgent(ag, ses)

	tokens := ag.ContextManager().TokenCount()
	// The stale usage baseline (2300) is far below the restored estimate;
	// the clamp must keep the display at the estimate, not the stub value.
	if tokens <= 2300 {
		t.Fatalf("stale usage baseline not clamped: TokenCount()=%d, want > 2300 (calibrated estimate of restored context)", tokens)
	}
}

// TestRestoreSessionFreshUsageBaselineKept verifies the clamp does NOT fire
// when the last usage is consistent with the restored context -- the real
// measurement still wins (accuracy over estimation).
func TestRestoreSessionFreshUsageBaselineKept(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("a", 500)}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("b", 500)}}},
	}

	ses := &session.Session{
		ID:              "fresh-usage-session",
		ContextMessages: msgs,
		UsageHistory: []session.UsageEntry{{
			Usage: provider.TokenUsage{InputTokens: 400, OutputTokens: 100},
		}},
	}

	RestoreSessionIntoAgent(ag, ses)

	// 500 is within 2x of the ~250-token estimate: measurement must be kept.
	if got := ag.ContextManager().TokenCount(); got != 500 {
		t.Fatalf("consistent usage baseline should be kept: TokenCount()=%d, want 500", got)
	}
}
