package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestIssue1048_ToolResultsPersistedAsUserRole(t *testing.T) {
	toolCalls := []ContentBlock{
		{Type: "tool_use", ToolName: "read_file", ToolID: "t1"},
		{Type: "tool_result", ToolID: "t1", Output: "file body"},
	}
	var assistantContent, toolResults []ContentBlock
	assistantContent = append(assistantContent, ContentBlock{Type: "text", Text: "done"})
	for _, tc := range toolCalls {
		if tc.Type == "tool_result" {
			toolResults = append(toolResults, tc)
		} else {
			assistantContent = append(assistantContent, tc)
		}
	}
	if len(toolResults) != 1 || toolResults[0].Type != "tool_result" {
		t.Fatalf("tool_result must be split out, got %+v", toolResults)
	}
	for _, b := range assistantContent {
		if b.Type == "tool_result" {
			t.Fatal("assistant message must not contain tool_result blocks (#1048)")
		}
	}
}

func TestIssue1051_CloseAfterEarlyStartFailure(t *testing.T) {
	c := NewClient(DiscoveredAgent{
		Def: AgentDef{Name: "no-such-agent", ACPCommand: []string{"/nonexistent/ggcode-agent-binary-xyz"}},
	}, t.TempDir(), nil, nil)
	if err := c.Start(context.Background()); err == nil {
		c.Close()
		t.Fatal("expected Start to fail for nonexistent binary")
	}
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close() hangs after early Start failure (#1051)")
	}
}

func TestIssue1053_SessionUpdateStopReasonFeedsPromptDone(t *testing.T) {
	c := NewClient(DiscoveredAgent{
		Def: AgentDef{Name: "x", ACPCommand: []string{"x"}},
	}, t.TempDir(), nil, nil)

	for _, r := range []StopReason{StopReasonEndTurn, StopReasonMaxTurns, StopReasonCancelled, StopReasonError} {
		if !terminalStopReason(r) {
			t.Fatalf("%s must be terminal", r)
		}
	}
	if terminalStopReason(StopReasonToolUse) || terminalStopReason("") {
		t.Fatal("tool_use/empty must not be terminal")
	}

	c.promptMu.Lock()
	c.activePromptID = "sess-1"
	c.promptDone = make(chan PromptResponse, 1)
	c.promptMu.Unlock()

	params, _ := json.Marshal(SessionNotification{
		SessionID: "sess-1",
		Update:    SessionUpdate{Type: UpdateAgentMessageChunk, StopReason: StopReasonEndTurn},
	})
	c.handleSessionUpdate(&JSONRPCRequest{Method: "session/update", Params: params})

	select {
	case resp := <-c.promptDone:
		if resp.StopReason != StopReasonEndTurn {
			t.Fatalf("got stop reason %q", resp.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal session/update stopReason never fed promptDone (#1053)")
	}
}
