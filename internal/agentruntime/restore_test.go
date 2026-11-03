package agentruntime

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
)

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

	runAdded := ag.AddedSinceRunStart()
	if len(runAdded) != 0 {
		t.Fatalf("runAdded should be empty after restore, got %d messages (first: role=%s)", len(runAdded), runAdded[0].Role)
	}
}

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

func TestRestoreSessionWithCheckpoint(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

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
		CheckpointMessageCount: len(allMsgs),
	}

	RestoreSessionIntoAgent(ag, ses)

	if runAdded := ag.AddedSinceRunStart(); len(runAdded) != 0 {
		t.Fatalf("runAdded should be empty after checkpoint restore, got %d", len(runAdded))
	}

	loadedMsgs := ag.Messages()

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

	tokens := ag.ContextManager().TokenCount()
	if tokens < 50000 {
		t.Fatalf("token count should be >= checkpoint baseline (50000), got %d", tokens)
	}
}

func TestRestoreNilAgent(t *testing.T) {

	_, _, _ = RestoreSessionIntoAgent(nil, &session.Session{ID: "test"})
	_, _, _ = RestoreSessionIntoAgent(&agent.Agent{}, nil)
}

func TestRestoreSessionStaleUsageBaselineClamped(t *testing.T) {
	reg := tool.NewRegistry()
	ag := agent.NewAgent(nil, reg, "system", 10)

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

	if tokens <= 2300 {
		t.Fatalf("stale usage baseline not clamped: TokenCount()=%d, want > 2300 (calibrated estimate of restored context)", tokens)
	}
}

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

	if got := ag.ContextManager().TokenCount(); got != 500 {
		t.Fatalf("consistent usage baseline should be kept: TokenCount()=%d, want 500", got)
	}
}
