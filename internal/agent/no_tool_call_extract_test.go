package agent

// Pin tests for the r126 extraction of the no-toolCalls branch of
// RunStreamWithContent into (*Agent).handleNoToolCallResponse. The method
// was moved verbatim; these tests lock the three externally observable
// control-flow outcomes (continue / stop) and the state propagation
// contract (counter pointers, asyncVerifyStats pointer write-through).

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// newNoToolCallTestAgent builds a minimal agent for direct
// handleNoToolCallResponse invocations.
func newNoToolCallTestAgent(t *testing.T) *Agent {
	t.Helper()
	return NewAgent(&mockProvider{}, tool.NewRegistry(), "System prompt", 1)
}

// noToolCallArgs bundles the per-call arguments of handleNoToolCallResponse
// so each pin test only overrides what it exercises.
type noToolCallArgs struct {
	resp                 *provider.ChatResponse
	textBuf              string
	toolCalls            []provider.ToolCallDelta
	truncated            bool
	policyBlocked        bool
	i                    int
	runStats             *RunStats
	userPromptForStats   string
	truncationContinues  int
	inlineToolCallNudges int
	todoCheckCount       int
	syncVerifyRetries    int
}

func (a *Agent) callHandleNoToolCall(t *testing.T, args noToolCallArgs) (cont bool, truncationContinues, inlineToolCallNudges, todoCheckCount, syncVerifyRetries int, asyncVerifyStats *RunStats, systemEvents []string) {
	t.Helper()
	truncationContinues = args.truncationContinues
	inlineToolCallNudges = args.inlineToolCallNudges
	todoCheckCount = args.todoCheckCount
	syncVerifyRetries = args.syncVerifyRetries
	cont = a.handleNoToolCallResponse(
		context.Background(),
		func(ev provider.StreamEvent) {
			if ev.Type == provider.StreamEventSystem {
				systemEvents = append(systemEvents, ev.Text)
			}
		},
		args.resp,
		args.textBuf,
		args.toolCalls,
		args.truncated,
		args.policyBlocked,
		args.i,
		args.runStats,
		args.userPromptForStats,
		&truncationContinues,
		&inlineToolCallNudges,
		&todoCheckCount,
		&syncVerifyRetries,
		&asyncVerifyStats,
	)
	return cont, truncationContinues, inlineToolCallNudges, todoCheckCount, syncVerifyRetries, asyncVerifyStats, systemEvents
}

func plainChatResp(text string) *provider.ChatResponse {
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: text}},
		},
	}
}

func lastMessages(msgs []provider.Message, n int) []provider.Message {
	if len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

func messageText(m provider.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

// TestHandleNoToolCallResponse_TruncationRecoveryContinues pins the
// truncated-response auto-continue path: increments the attempt counter,
// keeps the partial assistant output in history, injects the continuation
// prompt, emits the system notice, and reports continueLoop=true.
func TestHandleNoToolCallResponse_TruncationRecoveryContinues(t *testing.T) {
	a := newNoToolCallTestAgent(t)
	stats := newRunStats("hi")
	_, truncationContinues, _, _, _, _, sysEvents := a.callHandleNoToolCall(t, noToolCallArgs{
		resp:      plainChatResp("partial ans"),
		textBuf:   "partial ans",
		truncated: true,
		runStats:  stats,
	})
	if truncationContinues != 1 {
		t.Fatalf("truncationContinues = %d, want 1", truncationContinues)
	}
	if len(sysEvents) != 1 || !strings.Contains(sysEvents[0], "truncated by output length limit") {
		t.Fatalf("expected truncation system event, got %q", sysEvents)
	}
	msgs := lastMessages(a.ContextManager().Messages(), 2)
	if len(msgs) != 2 || msgs[0].Role != "assistant" || msgs[1].Role != "user" {
		t.Fatalf("expected assistant+continuation-user messages, got %+v", msgs)
	}
	if !strings.Contains(messageText(msgs[1]), "Continue from where you left off") {
		t.Fatalf("continuation prompt missing, got %q", messageText(msgs[1]))
	}
}

// TestHandleNoToolCallResponse_TruncationCapExhaustedStops pins that once
// the 3-attempt truncation budget is burned, the run falls through to the
// completion path (continueLoop=false) and still captures asyncVerifyStats.
func TestHandleNoToolCallResponse_TruncationCapExhaustedStops(t *testing.T) {
	a := newNoToolCallTestAgent(t)
	stats := newRunStats("hi")
	cont, truncationContinues, _, _, _, asyncVerifyStats, _ := a.callHandleNoToolCall(t, noToolCallArgs{
		resp:                plainChatResp("partial ans"),
		textBuf:             "partial ans",
		truncated:           true,
		i:                   4,
		runStats:            stats,
		truncationContinues: 3,
	})
	if cont {
		t.Fatal("expected continueLoop=false after truncation budget exhausted")
	}
	if truncationContinues != 3 {
		t.Fatalf("truncationContinues = %d, want unchanged 3", truncationContinues)
	}
	if asyncVerifyStats != stats {
		t.Fatal("asyncVerifyStats not written through pointer on stop path")
	}
}

// TestHandleNoToolCallResponse_InlineToolCallNudgeContinues pins the inline
// tool-call nudge path: bumps the nudge counter, stores the assistant
// message plus the format-correction prompt, and reports continueLoop=true.
func TestHandleNoToolCallResponse_InlineToolCallNudgeContinues(t *testing.T) {
	a := newNoToolCallTestAgent(t)
	stats := newRunStats("hi")
	inlineJSON := `{"name": "run_command", "arguments": {"command": "ls -la /tmp"}}`
	cont, _, inlineToolCallNudges, _, _, _, _ := a.callHandleNoToolCall(t, noToolCallArgs{
		resp:     plainChatResp(inlineJSON),
		textBuf:  inlineJSON,
		runStats: stats,
	})
	if !cont {
		t.Fatal("expected continueLoop=true on inline tool call nudge")
	}
	if inlineToolCallNudges != 1 {
		t.Fatalf("inlineToolCallNudges = %d, want 1", inlineToolCallNudges)
	}
	msgs := lastMessages(a.ContextManager().Messages(), 2)
	if len(msgs) != 2 || msgs[1].Role != "user" || !strings.Contains(messageText(msgs[1]), "structured tool_use format") {
		t.Fatalf("expected nudge user message, got %+v", msgs)
	}
}

// TestHandleNoToolCallResponse_PlainTextStops pins the plain-text terminal
// path: no counter moves, the assistant message is stored, asyncVerifyStats
// is captured, and continueLoop=false makes the caller return nil.
func TestHandleNoToolCallResponse_PlainTextStops(t *testing.T) {
	a := newNoToolCallTestAgent(t)
	stats := newRunStats("hi")
	cont, truncationContinues, nudges, todoChecks, syncRetries, asyncVerifyStats, _ := a.callHandleNoToolCall(t, noToolCallArgs{
		resp:     plainChatResp("All tasks are complete."),
		textBuf:  "All tasks are complete.",
		i:        2,
		runStats: stats,
	})
	if cont {
		t.Fatal("expected continueLoop=false for plain text response")
	}
	if truncationContinues != 0 || nudges != 0 || todoChecks != 0 || syncRetries != 0 {
		t.Fatalf("counters moved on plain path: trunc=%d nudges=%d todo=%d sync=%d", truncationContinues, nudges, todoChecks, syncRetries)
	}
	if asyncVerifyStats != stats {
		t.Fatal("asyncVerifyStats not captured on plain stop path")
	}
	msgs := a.ContextManager().Messages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != "assistant" || !strings.Contains(messageText(msgs[len(msgs)-1]), "All tasks are complete.") {
		t.Fatalf("assistant message not stored, got %+v", msgs)
	}
}

// TestRunStreamWithContent_NoToolCallResponseReturnsNil pins the caller
// wiring end-to-end: a streamed text-only response terminates the run with
// a nil error through the extracted method.
func TestRunStreamWithContent_NoToolCallResponseReturnsNil(t *testing.T) {
	mp := &mockProvider{
		chatResponses: []*provider.ChatResponse{plainChatResp("All done.")},
		streamEvents: [][]provider.StreamEvent{{
			{Type: provider.StreamEventText, Text: "All done."},
			{Type: provider.StreamEventDone},
		}},
	}
	a := NewAgent(mp, tool.NewRegistry(), "System prompt", 1)
	err := a.RunStreamWithContent(context.Background(), []provider.ContentBlock{{Type: "text", Text: "finish the task"}}, func(provider.StreamEvent) {})
	if err != nil {
		t.Fatalf("RunStreamWithContent() error = %v, want nil", err)
	}
	msgs := a.ContextManager().Messages()
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != "assistant" || !strings.Contains(messageText(msgs[len(msgs)-1]), "All done.") {
		t.Fatalf("assistant response not in history, got %+v", msgs)
	}
}
