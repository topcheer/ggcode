package agent

import (
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

// llmTurnMetrics captures per-turn LLM streaming timing (TTFT, think time)
// for the turn-based metrics pipeline (#937).
//
// The first generated delta of ANY kind - text, reasoning, or tool-call
// chunk - stamps firstToken: pure tool turns (no text, no reasoning, the
// common agentic case) previously recorded TTFT=0, skewing per-turn stats
// and digest averages, and making 0 ambiguous (no-output vs tool-turn).
//
// Think windows open on the first non-empty reasoning delta and close on
// the first ToolCallDone or Done (whichever arrives first); multiple
// think-think-act cycles accumulate.
type llmTurnMetrics struct {
	start         time.Time
	firstToken    time.Time
	thinkStart    time.Time
	thinkDuration time.Duration
	hasFirstToken bool
}

func newLLMTurnMetrics() *llmTurnMetrics {
	return &llmTurnMetrics{start: time.Now()}
}

// markText stamps firstToken when this is the first non-empty text delta.
func (m *llmTurnMetrics) markText(text string) {
	if !m.hasFirstToken && text != "" {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
}

// markReasoning stamps firstToken and opens the think window on the first
// non-empty reasoning delta.
func (m *llmTurnMetrics) markReasoning(text string) {
	if !m.hasFirstToken && text != "" {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
	if text != "" && m.thinkStart.IsZero() {
		m.thinkStart = time.Now()
	}
}

// markToolChunk stamps firstToken on the first tool-call delta. Early
// chunks may carry an empty tool name - they are still generated output.
func (m *llmTurnMetrics) markToolChunk() {
	if !m.hasFirstToken {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
}

// closeThinkWindow accumulates the open think window (if any) into
// thinkDuration. Called on ToolCallDone and Done.
func (m *llmTurnMetrics) closeThinkWindow() {
	if !m.thinkStart.IsZero() {
		m.thinkDuration += time.Since(m.thinkStart)
		m.thinkStart = time.Time{}
	}
}

// emit builds the "llm" metric event for a completed turn. ttft is zero
// only when the stream produced no generated output at all.
func (m *llmTurnMetrics) emit(usage provider.TokenUsage) metrics.MetricEvent {
	m.closeThinkWindow()
	now := time.Now()
	ttft := time.Duration(0)
	if m.hasFirstToken {
		ttft = m.firstToken.Sub(m.start)
	}
	return metrics.MetricEvent{
		Timestamp:    now,
		Type:         "llm",
		TTFT:         ttft,
		ThinkTime:    m.thinkDuration,
		Duration:     now.Sub(m.start),
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		CacheRead:    usage.CacheRead,
		CacheWrite:   usage.CacheWrite,
	}
}
