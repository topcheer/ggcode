package metrics

import "time"

// MetricEvent records a single performance measurement — either an LLM API call
// or a tool execution. Designed to be fire-and-forget: the agent emits these
// via a callback, and the caller persists them asynchronously.
type MetricEvent struct {
	Timestamp time.Time `json:"timestamp"`
	TurnIndex int       `json:"turn_index"`
	Type      string    `json:"type"` // "llm" or "tool"

	// LLM metrics
	TTFT      time.Duration `json:"ttft,omitempty"`       // time to first text/reasoning token
	ThinkTime time.Duration `json:"think_time,omitempty"` // cumulative reasoning/thinking duration
	Duration  time.Duration `json:"duration,omitempty"`   // total LLM call wall time

	// Tool metrics
	ToolName     string        `json:"tool_name,omitempty"`
	ToolSuccess  bool          `json:"tool_success,omitempty"`
	ToolError    string        `json:"tool_error,omitempty"`
	ToolDuration time.Duration `json:"tool_duration,omitempty"`

	// Metadata
	Model    string `json:"model,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}
