package im

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

// EvalMetrics collects quantitative metrics during an evaluation session.
// #1302: written concurrently by agent event goroutines (RecordEvent via
// fanOutSend) and HTTP handlers (Reset, UserMessages), read by /status
// (Snapshot) - every method now holds mu, and Snapshot deep-copies maps so
// callers never race the originals.
type EvalMetrics struct {
	mu sync.Mutex

	SessionStart time.Time
	SessionEnd   time.Time

	UserMessages     int
	AskUserCount     int
	AskUserLatencyMs map[string]int64 // question_id → latency

	ToolCalls        map[string]int // tool_name → count
	TotalToolCalls   int
	ToolErrors       int
	ToolErrorsByTool map[string]int // tool_name → error count
	ReworkCount      int
	Rounds           int

	InputTokens  int
	OutputTokens int

	KnightReports int
	StagedSkills  int

	ElapsedMs int64

	lastToolName string
	askStart     map[string]time.Time // question_id → start time
}

// roundPattern matches round summary text. Requires a digit AND "tool" keyword
// to avoid false positives. Coupled with EmitRoundSummary (emitter.go:229).
var roundPattern = regexp.MustCompile(`(?i)\d+.*tool|tool.*\d+`)

// NewEvalMetrics creates a new metrics collector.
func NewEvalMetrics() *EvalMetrics {
	return &EvalMetrics{
		SessionStart:     time.Now(),
		ToolCalls:        make(map[string]int),
		ToolErrorsByTool: make(map[string]int),
		AskUserLatencyMs: make(map[string]int64),
		askStart:         make(map[string]time.Time),
	}
}

// RecordEvent updates metrics based on an outbound event.
func (m *EvalMetrics) RecordEvent(event OutboundEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch event.Kind {
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return
		}
		name := event.ToolRes.ToolName
		m.ToolCalls[name]++
		m.TotalToolCalls++

		// Rework detection: consecutive calls to the same tool
		if m.lastToolName == name {
			m.ReworkCount++
		}
		m.lastToolName = name

		if event.ToolRes.IsError {
			m.ToolErrors++
			m.ToolErrorsByTool[name]++
		}

	case OutboundEventText:
		text := event.Text
		if strings.HasPrefix(text, "🌙 ") {
			m.KnightReports++
		}
		if roundPattern.MatchString(text) {
			m.Rounds++
		}

	case OutboundEventApprovalRequest:
		m.AskUserCount++
	}
}

// IncUserMessages increments the user-message counter under the lock
// (#1302: HTTP handlers previously incremented the field directly,
// racing Snapshot's read).
func (m *EvalMetrics) IncUserMessages() {
	m.mu.Lock()
	m.UserMessages++
	m.mu.Unlock()
}

// Reset clears all per-task metrics while keeping session-level fields.
func (m *EvalMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UserMessages = 0
	m.AskUserCount = 0
	m.AskUserLatencyMs = make(map[string]int64)
	m.ToolCalls = make(map[string]int)
	m.TotalToolCalls = 0
	m.ToolErrors = 0
	m.ToolErrorsByTool = make(map[string]int)
	m.ReworkCount = 0
	m.Rounds = 0
	m.InputTokens = 0
	m.OutputTokens = 0
	m.KnightReports = 0
	m.StagedSkills = 0
	m.ElapsedMs = 0
	m.lastToolName = ""
	m.askStart = make(map[string]time.Time)
}

// Snapshot returns a copy of current metrics as a map.
func (m *EvalMetrics) Snapshot() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	toolCalls := make(map[string]int, len(m.ToolCalls))
	for k, v := range m.ToolCalls {
		toolCalls[k] = v
	}
	toolErrorsBy := make(map[string]int, len(m.ToolErrorsByTool))
	for k, v := range m.ToolErrorsByTool {
		toolErrorsBy[k] = v
	}
	return map[string]interface{}{
		"total_tool_calls": m.TotalToolCalls,
		"tool_calls":       toolCalls,
		"tool_errors":      m.ToolErrors,
		"tool_errors_by":   toolErrorsBy,
		"rework_count":     m.ReworkCount,
		"rounds":           m.Rounds,
		"ask_user_count":   m.AskUserCount,
		"user_messages":    m.UserMessages,
		"knight_reports":   m.KnightReports,
		"staged_skills":    m.StagedSkills,
		"elapsed_ms":       m.ElapsedMs,
		"input_tokens":     m.InputTokens,
		"output_tokens":    m.OutputTokens,
	}
}
