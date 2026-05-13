package main

import (
	"fmt"
	"runtime/debug"
	"strings"
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

	// Assistant streaming buffer.
	assistantBuf strings.Builder

	// Non-bound state: chat messages.
	ChatMsgs  []ChatMessage
	ChatMu    sync.Mutex
	ChatDirty bool

	// Agent panel state (sub-agents + teammates).
	agentMu     sync.Mutex
	agentPanels map[string]AgentPanelData
	agentDirty  bool
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

// AppendAssistantText appends a streaming text chunk to the assistant buffer
// and updates (or creates) the last assistant message with the full accumulated text.
func (u *UIState) AppendAssistantText(chunk string) {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	u.assistantBuf.WriteString(chunk)
	full := u.assistantBuf.String()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "assistant" && u.ChatMsgs[i].Streaming {
			u.ChatMsgs[i].Content = full
			u.ChatDirty = true
			return
		}
	}
	// No streaming assistant message yet, create one.
	u.ChatMsgs = append(u.ChatMsgs, ChatMessage{
		Role:      "assistant",
		Content:   full,
		Streaming: true,
	})
	u.ChatDirty = true
}

// UpdateToolResult updates the tool message with matching tool call ID.
func (u *UIState) UpdateToolResult(toolID, result string, isError bool) {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "tool" && u.ChatMsgs[i].ToolID == toolID {
			u.ChatMsgs[i].Content = result
			u.ChatMsgs[i].IsError = isError
			u.ChatDirty = true
			return
		}
	}
}

// FinalizeStreaming marks the last streaming assistant message as done,
// resets the streaming buffer, and marks any still-running tool calls as cancelled.
func (u *UIState) FinalizeStreaming() {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	u.assistantBuf.Reset()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "assistant" && u.ChatMsgs[i].Streaming {
			u.ChatMsgs[i].Streaming = false
		}
	}
	// Mark any tool messages still showing "running" (empty content) as cancelled.
	for i := range u.ChatMsgs {
		if u.ChatMsgs[i].Role == "tool" && u.ChatMsgs[i].Content == "" {
			u.ChatMsgs[i].Content = "cancelled"
			u.ChatMsgs[i].IsError = true
		}
	}
	u.ChatDirty = true
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

// ── Agent panel state ────────────────────────────────

// agentPanels stores sub-agent/teammate panel data keyed by ID.
// Protected by its own mutex since updates come from callbacks.
func (u *UIState) UpdateAgentPanel(id string, data AgentPanelData) {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	if u.agentPanels == nil {
		u.agentPanels = make(map[string]AgentPanelData)
	}
	u.agentPanels[id] = data
	u.agentDirty = true
}

func (u *UIState) GetAgentPanels() []AgentPanelData {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	out := make([]AgentPanelData, 0, len(u.agentPanels))
	for _, p := range u.agentPanels {
		out = append(out, p)
	}
	return out
}

func (u *UIState) IsAgentDirty() bool {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	return u.agentDirty
}

func (u *UIState) ClearAgentDirty() {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	u.agentDirty = false
}
