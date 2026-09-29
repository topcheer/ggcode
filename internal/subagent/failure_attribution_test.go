package subagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// toolEvents builds a compact event trail: n tool calls, with the entries of
// errIdxs (0-based call ordinals) recorded as errored tool results.
func toolEvents(n int, errIdxs map[int]string) []AgentEvent {
	events := make([]AgentEvent, 0, n*2)
	for i := 0; i < n; i++ {
		events = append(events, AgentEvent{
			Type:     AgentEventToolCall,
			ToolName: "bash",
			ToolID:   fmt.Sprintf("t%d", i),
			ToolArgs: fmt.Sprintf("go build ./pkg%d", i),
		})
		msg, isErr := fmt.Sprintf("ok %d", i), false
		if errClass, bad := errIdxs[i]; bad {
			msg, isErr = errClass, true
		}
		events = append(events, AgentEvent{
			Type:     AgentEventToolResult,
			ToolName: "bash",
			ToolID:   fmt.Sprintf("t%d", i),
			Result:   msg,
			IsError:  isErr,
		})
	}
	return events
}

func TestAttributeFailureToolErrorLoop(t *testing.T) {
	events := toolEvents(6, map[int]string{3: "exit status 1", 4: "exit status 1", 5: "exit status 1"})
	attr := attributeFailure(events, 0)
	if attr == nil {
		t.Fatal("expected attribution, got nil")
	}
	if attr.Category != FailCatToolErrorLoop {
		t.Fatalf("category = %q, want %q", attr.Category, FailCatToolErrorLoop)
	}
	if attr.ErrorToolCalls != 3 {
		t.Fatalf("ErrorToolCalls = %d, want 3", attr.ErrorToolCalls)
	}
	if attr.FirstErrorStep != 4 {
		t.Fatalf("FirstErrorStep = %d, want 4 (1-based)", attr.FirstErrorStep)
	}
	out := attr.String()
	if !strings.Contains(out, "step 4/6") {
		t.Fatalf("String() missing step info: %q", out)
	}
	if !strings.Contains(out, "bash(go build ./pkg5): exit status 1") {
		t.Fatalf("String() missing last-error sample: %q", out)
	}
	if !strings.Contains(out, "3 errors in last 5 calls") {
		t.Fatalf("String() missing loop summary: %q", out)
	}
}

func TestAttributeFailureScatteredToolError(t *testing.T) {
	// Errors early in the trail, clean tail: not a doomed loop.
	events := toolEvents(8, map[int]string{0: "exit status 1", 1: "exit status 1", 2: "exit status 1"})
	attr := attributeFailure(events, 0)
	if attr.Category != FailCatToolError {
		t.Fatalf("category = %q, want %q", attr.Category, FailCatToolError)
	}
}

func TestAttributeFailureLLMErrorOnly(t *testing.T) {
	events := []AgentEvent{
		{Type: AgentEventToolCall, ToolName: "read_file"},
		{Type: AgentEventToolResult, ToolName: "read_file", Result: "ok"},
		{Type: AgentEventError, Text: "stream reset by provider"},
	}
	attr := attributeFailure(events, 0)
	if attr.Category != FailCatLLMError {
		t.Fatalf("category = %q, want %q", attr.Category, FailCatLLMError)
	}
	if attr.ErrorToolCalls != 0 {
		t.Fatalf("ErrorToolCalls = %d, want 0", attr.ErrorToolCalls)
	}
	if attr.Sample != "stream reset by provider" {
		t.Fatalf("Sample = %q", attr.Sample)
	}
}

func TestAttributeFailureSilent(t *testing.T) {
	events := toolEvents(3, nil)
	attr := attributeFailure(events, 7)
	if attr.Category != FailCatSilent {
		t.Fatalf("category = %q, want %q", attr.Category, FailCatSilent)
	}
	if !strings.Contains(attr.String(), "+7 earlier events evicted") {
		t.Fatalf("String() missing eviction note: %q", attr.String())
	}
}

func TestAttributeFailureEmptyTrail(t *testing.T) {
	// Lifecycle rejections (never started / shutdown / limit) have no trail:
	// no attribution, the plain Error field already tells that story.
	if attr := attributeFailure(nil, 0); attr != nil {
		t.Fatalf("expected nil for empty trail, got %+v", attr)
	}
}

func TestRenderErrorEventTruncation(t *testing.T) {
	long := strings.Repeat("x", 300)
	ev := AgentEvent{Type: AgentEventToolResult, ToolName: "bash", ToolArgs: long, Result: long}
	out := renderErrorEvent(ev)
	if len(out) > attributionMaxArgs+attributionMaxErr+64 {
		t.Fatalf("render too long: %d chars", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected ASCII truncation marker, got %q", out[len(out)-8:])
	}
}

// CompleteAttachesAttributionOnFailure verifies the wiring: a failed
// terminal transition stamps the snapshot with the attribution line, while
// successful runs stay clean.
func TestCompleteAttachesAttributionOnFailure(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()

	id := m.Spawn("builder", "build the project", "build", nil, m.RootContext())
	m.SetCancel(id, func() {})

	sa, ok := m.agents[id]
	if !ok {
		t.Fatal("agent missing from manager")
	}
	sa.appendEvent(AgentEvent{Type: AgentEventToolCall, ToolName: "bash", ToolID: "t1", ToolArgs: "make test"})
	sa.appendEvent(AgentEvent{Type: AgentEventToolResult, ToolName: "bash", ToolID: "t1", Result: "FAIL", IsError: true})
	sa.appendEvent(AgentEvent{Type: AgentEventError, Text: "llm connection refused"})

	m.Complete(id, "", fmt.Errorf("exit status 1"))

	snap, ok := m.Snapshot(id)
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snap.Status != StatusFailed {
		t.Fatalf("status = %q, want failed", snap.Status)
	}
	if snap.FailureAttribution == "" {
		t.Fatal("FailureAttribution empty on failed snapshot")
	}
	if !strings.HasPrefix(snap.FailureAttribution, FailCatToolError) {
		t.Fatalf("attribution = %q, want %q prefix", snap.FailureAttribution, FailCatToolError)
	}

	// A successful run must NOT carry an attribution.
	okID := m.Spawn("writer", "write docs", "write", nil, m.RootContext())
	m.SetCancel(okID, func() {})
	m.Complete(okID, "done", nil)
	okSnap, ok2 := m.Snapshot(okID)
	if !ok2 {
		t.Fatal("ok snapshot missing")
	}
	if okSnap.FailureAttribution != "" {
		t.Fatalf("successful snapshot carries attribution %q", okSnap.FailureAttribution)
	}
}

// CompleteTerminalDoesNotRecompute ensures the terminal-state guard keeps a
// pre-existing attribution intact when a late Complete races in after a
// cancel.
func TestCompleteTerminalDoesNotRecompute(t *testing.T) {
	m := NewManager(config.SubAgentConfig{})
	defer m.Shutdown()

	id := m.Spawn("late", "x", "x", nil, m.RootContext())
	m.SetCancel(id, func() {})
	sa := m.agents[id]
	sa.appendEvent(AgentEvent{Type: AgentEventToolCall, ToolName: "bash"})

	m.Complete(id, "partial output", context.Canceled)
	first, _ := m.Snapshot(id)
	// A late successful Complete must not flip the status nor invent
	// attribution for a run already terminal.
	m.Complete(id, "late result", nil)
	second, _ := m.Snapshot(id)
	if second.Status != StatusFailed {
		t.Fatalf("status flipped to %q after late Complete", second.Status)
	}
	if second.FailureAttribution != first.FailureAttribution {
		t.Fatalf("attribution recomputed on terminal re-entry: %q -> %q",
			first.FailureAttribution, second.FailureAttribution)
	}
	if second.Result != "partial output" && second.Result != "" && second.Result != "late result" {
		t.Fatalf("unexpected backfilled result %q", second.Result)
	}
}
