package agent

import (
	"time"

	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/provider"
)

type llmTurnMetrics struct {
	start         time.Time
	firstToken    time.Time
	thinkStart    time.Time
	thinkDuration time.Duration
	hasFirstToken bool
}

func stampMetricModel(p provider.Provider, ev *metrics.MetricEvent) {
	if mp, ok := p.(provider.ModelNameProvider); ok {
		if name := mp.ModelName(); name != "" {
			ev.Model = name
		}
	}
}

func newLLMTurnMetrics() *llmTurnMetrics {
	return &llmTurnMetrics{start: time.Now()}
}

func (m *llmTurnMetrics) markText(text string) {
	if !m.hasFirstToken && text != "" {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
}

func (m *llmTurnMetrics) markReasoning(text string) {
	if !m.hasFirstToken && text != "" {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
	if text != "" && m.thinkStart.IsZero() {
		m.thinkStart = time.Now()
	}
}

func (m *llmTurnMetrics) markToolChunk() {
	if !m.hasFirstToken {
		m.firstToken = time.Now()
		m.hasFirstToken = true
	}
}

func (m *llmTurnMetrics) closeThinkWindow() {
	if !m.thinkStart.IsZero() {
		m.thinkDuration += time.Since(m.thinkStart)
		m.thinkStart = time.Time{}
	}
}

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
