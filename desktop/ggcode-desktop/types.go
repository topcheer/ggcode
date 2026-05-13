package main

import "time"

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role      string // "user", "assistant", "system", "tool", "reasoning", "error"
	Content   string
	ToolName  string
	ToolArgs  string
	Time      time.Time
	Streaming bool
}
