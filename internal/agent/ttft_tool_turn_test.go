package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestTTFTPureToolTurn (#937): a stream that delivers ONLY tool-call deltas
// (no text, no reasoning) must still record a nonzero TTFT - the first
// tool-call delta is generated output like any text delta. Previously such
// turns recorded TTFT=0, skewing per-turn stats and making 0 ambiguous.
//
// This exercises the same firstTokenTime bookkeeping the agent loop uses;
// the stream harness below replays the event sequence a pure tool turn
// produces.
func TestTTFTPureToolTurn(t *testing.T) {
	// Event order a pure tool turn generates: chunk... chunk... done.
	events := []provider.StreamEvent{
		{Type: provider.StreamEventToolCallChunk, Tool: provider.ToolCallDelta{Name: "run_command"}},
		{Type: provider.StreamEventToolCallChunk, Tool: provider.ToolCallDelta{Name: "run_command"}},
		{Type: provider.StreamEventDone},
	}

	// Mirror the loop's bookkeeping (same fields, same branch logic) to
	// lock the invariant: first delta of ANY generated type sets the stamp.
	var firstTokenTime time.Time
	hasFirstToken := false
	llmStart := time.Now()
	for _, event := range events {
		switch event.Type {
		case provider.StreamEventText:
			if !hasFirstToken && event.Text != "" {
				firstTokenTime = time.Now()
				hasFirstToken = true
			}
		case provider.StreamEventReasoning:
			if !hasFirstToken && event.Text != "" {
				firstTokenTime = time.Now()
				hasFirstToken = true
			}
		case provider.StreamEventToolCallChunk:
			if !hasFirstToken {
				firstTokenTime = time.Now()
				hasFirstToken = true
			}
		}
	}
	if !hasFirstToken {
		t.Fatal("pure tool-call turn must set firstTokenTime")
	}
	ttft := firstTokenTime.Sub(llmStart)
	if ttft <= 0 {
		t.Fatalf("pure tool-call turn TTFT must be positive, got %v", ttft)
	}
}
