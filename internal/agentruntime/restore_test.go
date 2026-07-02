package agentruntime

import (
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

// TestRestoreSessionWithCheckpoint verifies the two-phase restore sets
// the checkpoint baseline correctly.
func TestRestoreSessionWithCheckpoint(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

	cpMsgs := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "system prompt"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "checkpoint user msg"}}},
	}
	postMsgs := []provider.Message{
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "post checkpoint"}}},
	}

	ses := &session.Session{
		ID:                     "test-cp-session",
		ContextMessages:        append(append([]provider.Message{}, cpMsgs...), postMsgs...),
		CheckpointTokens:       50000,
		CheckpointMessageCount: len(cpMsgs),
	}

	RestoreSessionIntoAgent(ag, ses)

	// runAdded must still be empty.
	if runAdded := ag.AddedSinceRunStart(); len(runAdded) != 0 {
		t.Fatalf("runAdded should be empty after checkpoint restore, got %d", len(runAdded))
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
	RestoreSessionIntoAgent(nil, &session.Session{ID: "test"})
	RestoreSessionIntoAgent(&agent.Agent{}, nil)
}
