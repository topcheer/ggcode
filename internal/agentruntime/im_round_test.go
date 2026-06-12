package agentruntime

import "testing"

func TestIMRoundStateTracksCountsAndReset(t *testing.T) {
	var round IMRoundState
	round.AppendText("hello")
	round.NoteToolCall()
	round.NoteToolResult(false)
	round.NoteToolResult(true)

	if got := round.Text(); got != "hello" {
		t.Fatalf("expected text to accumulate, got %q", got)
	}
	if round.ToolCalls != 1 || round.ToolSuccesses != 1 || round.ToolFailures != 1 {
		t.Fatalf("unexpected counters: %+v", round)
	}

	round.Reset()
	if got := round.Text(); got != "" {
		t.Fatalf("expected reset text, got %q", got)
	}
	if round.ToolCalls != 0 || round.ToolSuccesses != 0 || round.ToolFailures != 0 {
		t.Fatalf("expected reset counters, got %+v", round)
	}
}
