package im

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

type EvalMetrics struct {
	mu sync.Mutex

	SessionStart time.Time
	SessionEnd   time.Time

	UserMessages     int
	AskUserCount     int
	AskUserLatencyMs map[string]int64

	ToolCalls        map[string]int
	TotalToolCalls   int
	ToolErrors       int
	ToolErrorsByTool map[string]int
	ReworkCount      int
	Rounds           int

	InputTokens  int
	OutputTokens int

	KnightReports int
	StagedSkills  int

	ElapsedMs int64

	lastToolName string
	askStart     map[string]time.Time
}

var roundPattern = regexp.MustCompile(`(?i)\d+.*tool|tool.*\d+`)

func NewEvalMetrics() *EvalMetrics {
	return &EvalMetrics{
		SessionStart:     time.Now(),
		ToolCalls:        make(map[string]int),
		ToolErrorsByTool: make(map[string]int),
		AskUserLatencyMs: make(map[string]int64),
		askStart:         make(map[string]time.Time),
	}
}

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

func (m *EvalMetrics) IncUserMessages() {
	m.mu.Lock()
	m.UserMessages++
	m.mu.Unlock()
}

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
