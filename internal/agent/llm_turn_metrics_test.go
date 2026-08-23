package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestLLMTurnMetricsPureToolTurn: a stream with ONLY tool-call deltas must
// produce a positive TTFT (#937) - previously such turns recorded 0.
func TestLLMTurnMetricsPureToolTurn(t *testing.T) {
	m := newLLMTurnMetrics()
	time.Sleep(2 * time.Millisecond) // ensure a measurable gap; wall-clock resolution can round sub-ms to 0
	m.markToolChunk()
	m.markToolChunk()
	ev := m.emit(provider.TokenUsage{InputTokens: 10, OutputTokens: 5})
	if ev.TTFT <= 0 {
		t.Fatalf("pure tool turn TTFT must be positive, got %v", ev.TTFT)
	}
	if ev.ThinkTime != 0 {
		t.Fatalf("no reasoning means zero think time, got %v", ev.ThinkTime)
	}
	if ev.OutputTokens != 5 || ev.InputTokens != 10 {
		t.Fatalf("usage passthrough broken: %+v", ev)
	}
	if ev.Type != "llm" {
		t.Fatalf("event type must be llm, got %q", ev.Type)
	}
}

// TestLLMTurnMetricsFirstDeltaWins: whichever generated delta arrives first
// (text, reasoning, or tool chunk) stamps TTFT; later kinds must not move it.
func TestLLMTurnMetricsFirstDeltaWins(t *testing.T) {
	m := newLLMTurnMetrics()
	m.markReasoning("think")
	m.markText("hello") // later, must not re-stamp
	m.markToolChunk()   // even later
	ev := m.emit(provider.TokenUsage{})
	if ev.TTFT <= 0 {
		t.Fatalf("TTFT must be positive, got %v", ev.TTFT)
	}
}

// TestLLMTurnMetricsEmptyDeltasDontStamp: zero-length text/reasoning deltas
// (keepalives) must not stamp firstToken.
func TestLLMTurnMetricsEmptyDeltasDontStamp(t *testing.T) {
	m := newLLMTurnMetrics()
	m.markText("")
	m.markReasoning("")
	ev := m.emit(provider.TokenUsage{})
	if ev.TTFT != 0 {
		t.Fatalf("no real output means zero TTFT, got %v", ev.TTFT)
	}
}

// TestLLMTurnMetricsThinkWindowAccumulates: reasoning opens the window,
// ToolCallDone closes it; a second think cycle accumulates into the total.
func TestLLMTurnMetricsThinkWindowAccumulates(t *testing.T) {
	m := newLLMTurnMetrics()
	m.markReasoning("first think")
	m.closeThinkWindow()
	m.markReasoning("second think")
	ev := m.emit(provider.TokenUsage{})
	if ev.ThinkTime <= 0 {
		t.Fatalf("two think cycles must accumulate positive think time, got %v", ev.ThinkTime)
	}
}
