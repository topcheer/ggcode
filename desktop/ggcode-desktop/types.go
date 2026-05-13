package main

import "time"

// ChatMessage represents a single message in the conversation.
type ChatMessage struct {
	Role      string // "user", "assistant", "system", "tool", "reasoning", "error"
	Content   string
	ToolName  string
	ToolDesc  string // human-readable description (from args.description or derived)
	ToolArgs  string // short summary of key arguments
	Time      time.Time
	Streaming bool
}
