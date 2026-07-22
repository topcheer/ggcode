package main

// Minimal record types for parsing JSONL session files.
// We only extract fields needed for the report — everything else is skipped.

import (
	"encoding/json"
	"time"
)

// jsonlRecord matches the top-level JSON structure of each line in a session
// JSONL file. Meta fields (title, workspace, model, etc.) are directly on the
// record, NOT nested under a "meta" key.
type jsonlRecord struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"session_id,omitempty"`
	Title       string          `json:"title,omitempty"`
	Workspace   string          `json:"workspace,omitempty"`
	Vendor      string          `json:"vendor,omitempty"`
	Endpoint    string          `json:"endpoint,omitempty"`
	Model       string          `json:"model,omitempty"`
	CreatedAt   time.Time       `json:"created_at,omitempty"`
	UpdatedAt   time.Time       `json:"updated_at,omitempty"`
	UsageEntry  json.RawMessage `json:"usage_entry,omitempty"`
	MetricEvent json.RawMessage `json:"metric_event,omitempty"`
}

type sessionMeta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Workspace string    `json:"workspace,omitempty"`
	Vendor    string    `json:"vendor"`
	Endpoint  string    `json:"endpoint"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type usageEntry struct {
	Timestamp time.Time `json:"timestamp"`
	TurnIndex int       `json:"turn_index"`
	Model     string    `json:"model,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	Usage     struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		CacheRead    int `json:"cache_read_tokens"`
		CacheWrite   int `json:"cache_write_tokens"`
	} `json:"usage"`
}

type metricEvent struct {
	TurnIndex    int           `json:"turn_index"`
	Type         string        `json:"type"` // "llm" or "tool"
	TTFT         time.Duration `json:"ttft,omitempty"`
	ThinkTime    time.Duration `json:"think_time,omitempty"`
	Duration     time.Duration `json:"duration,omitempty"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
	ToolName     string        `json:"tool_name,omitempty"`
	ToolSuccess  bool          `json:"tool_success,omitempty"`
	ToolDuration time.Duration `json:"tool_duration,omitempty"`
}
