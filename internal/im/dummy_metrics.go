package im

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EvalMetrics collects quantitative metrics during an evaluation session.
type EvalMetrics struct {
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

// Reset clears all per-task metrics while keeping session-level fields.
func (m *EvalMetrics) Reset() {
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
	return map[string]interface{}{
		"total_tool_calls": m.TotalToolCalls,
		"tool_calls":       m.ToolCalls,
		"tool_errors":      m.ToolErrors,
		"tool_errors_by":   m.ToolErrorsByTool,
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

// WriteCSV appends a metrics row to a CSV file.
func (m *EvalMetrics) WriteCSV(path string, runID, phase, mode string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	record := []string{
		runID,
		phase,
		mode,
		"", // provider_vendor
		"", // provider_endpoint
		"", // provider_model
		"", // task_set
		"", // task_id
		"", // success
		strconv.Itoa(m.ToolErrors),
		strconv.Itoa(m.TotalToolCalls),
		strconv.Itoa(m.UserMessages),
		strconv.Itoa(int(m.ElapsedMs / 1000)),
		strconv.Itoa(m.InputTokens),
		strconv.Itoa(m.OutputTokens),
		strconv.Itoa(m.StagedSkills),
		"", // patched_skills
		"", // rollbacks
		"", // notes
	}
	return w.Write(record)
}

// WriteJSON writes the metrics snapshot to a JSON file.
func (m *EvalMetrics) WriteJSON(path string) error {
	data, err := json.MarshalIndent(m.Snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
