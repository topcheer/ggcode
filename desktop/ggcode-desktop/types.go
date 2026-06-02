package main

import "time"

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role         string // "user", "assistant", "system", "tool", "reasoning", "error"
	Content      string
	ToolName     string
	ToolID       string // unique tool call ID for matching results
	ToolDesc     string // human-readable description (from args.description or derived)
	ToolArgs     string // short summary of key arguments
	ToolRaw      string // raw JSON arguments for field extraction
	Time         time.Time
	Streaming    bool
	IsError      bool // tool result was an error
	PreventMerge bool
}

// AgentPanelData holds the state for a sub-agent or teammate panel.
type AgentPanelData struct {
	ID          string // agent-xxx or tm-xxx
	Name        string // display name
	Kind        string // "subagent" or "teammate"
	Status      string // "running", "completed", "failed", "idle", "working"
	Task        string // task description
	Result      string // final result text
	Error       string // error message if any
	TeamID      string // swarm team ID (for teammates)
	CompletedAt time.Time

	Events []AgentEventEntry // ordered event stream
}

// AgentEventEntry is a single event in an agent's event stream.
type AgentEventEntry struct {
	Type            string // "text", "tool_call", "tool_result", "error"
	Content         string
	ToolName        string
	ToolID          string // unique tool call ID for precise matching
	ToolArgs        string
	ToolDisplayName string
	ToolDetail      string
	IsError         bool // for tool_result / error
}

// truncateRunes truncates a string to maxChars runes, appending suffix if truncated.
// Safe for multi-byte characters (Chinese, Japanese, etc.).
func truncateRunes(s string, maxChars int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + suffix
}
