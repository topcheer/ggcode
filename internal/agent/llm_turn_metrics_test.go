package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestLLMTurnMetricsPureToolTurn(t *testing.T) {
	m := newLLMTurnMetrics()
	time.Sleep(2 * time.Millisecond)
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

func TestLLMTurnMetricsFirstDeltaWins(t *testing.T) {
	m := newLLMTurnMetrics()

	time.Sleep(2 * time.Millisecond)
	m.markReasoning("think")
	m.markText("hello")
	m.markToolChunk()
	ev := m.emit(provider.TokenUsage{})
	if ev.TTFT <= 0 {
		t.Fatalf("TTFT must be positive, got %v", ev.TTFT)
	}
}

func TestLLMTurnMetricsEmptyDeltasDontStamp(t *testing.T) {
	m := newLLMTurnMetrics()
	m.markText("")
	m.markReasoning("")
	ev := m.emit(provider.TokenUsage{})
	if ev.TTFT != 0 {
		t.Fatalf("no real output means zero TTFT, got %v", ev.TTFT)
	}
}

func TestLLMTurnMetricsThinkWindowAccumulates(t *testing.T) {
	m := newLLMTurnMetrics()
	m.markReasoning("first think")

	time.Sleep(2 * time.Millisecond)
	m.closeThinkWindow()
	m.markReasoning("second think")
	time.Sleep(2 * time.Millisecond)
	ev := m.emit(provider.TokenUsage{})
	if ev.ThinkTime <= 0 {
		t.Fatalf("two think cycles must accumulate positive think time, got %v", ev.ThinkTime)
	}
}
