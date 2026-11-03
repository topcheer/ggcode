package metrics

import "time"

type MetricEvent struct {
	Timestamp time.Time `json:"timestamp"`
	TurnIndex int       `json:"turn_index"`
	Type      string    `json:"type"`

	TTFT      time.Duration `json:"ttft,omitempty"`
	ThinkTime time.Duration `json:"think_time,omitempty"`
	Duration  time.Duration `json:"duration,omitempty"`

	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	CacheRead    int `json:"cache_read_tokens,omitempty"`
	CacheWrite   int `json:"cache_write_tokens,omitempty"`

	ToolName     string        `json:"tool_name,omitempty"`
	ToolSuccess  bool          `json:"tool_success,omitempty"`
	ToolError    string        `json:"tool_error,omitempty"`
	ToolDuration time.Duration `json:"tool_duration,omitempty"`

	Model    string `json:"model,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`

	AgentID string `json:"agent_id,omitempty"`
}
