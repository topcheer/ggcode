package main

import (
	"fmt"
	"runtime/debug"
	"sync"

	"fyne.io/fyne/v2/data/binding"
)

// UIState holds bindings for cross-goroutine UI updates.
// Goroutines write to bindings; widgets read from them automatically.
type UIState struct {
	mu sync.RWMutex

	StatusText binding.String
	ModelName  binding.String
	TokenUsage binding.String
	ContextWin binding.String
	TokenPct   binding.Float

	// Non-bound state: chat messages.
	ChatMsgs  []ChatMessage
	ChatMu    sync.Mutex
	ChatDirty bool
}

func NewUIState() *UIState {
	s := &UIState{}
	s.StatusText = binding.NewString()
	s.ModelName = binding.NewString()
	s.TokenUsage = binding.NewString()
	s.ContextWin = binding.NewString()
	s.TokenPct = binding.NewFloat()
	_ = s.StatusText.Set("Ready")
	_ = s.ModelName.Set("")
	_ = s.TokenUsage.Set("")
	_ = s.ContextWin.Set("")
	_ = s.TokenPct.Set(0)
	return s
}

// SetStatus safely updates the status bar text.
func (u *UIState) SetStatus(text string) {
	_ = u.StatusText.Set(text)
}

// SetModelInfo safely updates model info bindings.
func (u *UIState) SetModelInfo(model, contextWin string) {
	_ = u.ModelName.Set(model)
	_ = u.ContextWin.Set(contextWin)
}

// SetTokenUsage safely updates token usage bindings.
func (u *UIState) SetTokenUsage(usage string, pct float64) {
	_ = u.TokenUsage.Set(usage)
	_ = u.TokenPct.Set(pct)
}

// AppendChat appends a message to the chat list (thread-safe).
// Returns true if the caller should trigger a UI refresh.
func (u *UIState) AppendChat(msg ChatMessage) bool {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	u.ChatMsgs = append(u.ChatMsgs, msg)
	u.ChatDirty = true
	return true
}

// UpdateLastAssistant updates the last assistant message (streaming).
func (u *UIState) UpdateLastAssistant(content string) {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "assistant" {
			u.ChatMsgs[i].Content = content
			u.ChatDirty = true
			return
		}
	}
	// No assistant message yet, create one.
	u.ChatMsgs = append(u.ChatMsgs, ChatMessage{
		Role:      "assistant",
		Content:   content,
		Streaming: true,
	})
	u.ChatDirty = true
}

// UpdateLastToolResult updates the last tool message with matching name.
func (u *UIState) UpdateLastToolResult(toolName, result string) {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "tool" && u.ChatMsgs[i].ToolName == toolName {
			u.ChatMsgs[i].Content = result
			u.ChatDirty = true
			return
		}
	}
}

// FinalizeStreaming marks the last streaming assistant message as done.
func (u *UIState) FinalizeStreaming() {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "assistant" && u.ChatMsgs[i].Streaming {
			u.ChatMsgs[i].Streaming = false
			return
		}
	}
}

// TakeMessages returns a snapshot of all messages and clears the dirty flag.
func (u *UIState) TakeMessages() []ChatMessage {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	u.ChatDirty = false
	out := make([]ChatMessage, len(u.ChatMsgs))
	copy(out, u.ChatMsgs)
	return out
}

// IsDirty returns whether the chat has pending updates.
func (u *UIState) IsDirty() bool {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	return u.ChatDirty
}

// CountMessages returns the current message count.
func (u *UIState) CountMessages() int {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	return len(u.ChatMsgs)
}

// safeRecover is a helper to log panics without crashing.
func safeRecover(context string) {
	if r := recover(); r != nil {
		fmt.Printf("[desktop] panic in %s: %v\n%s", context, r, debug.Stack())
	}
}
