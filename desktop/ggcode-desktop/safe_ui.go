package main

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
)

// ── Event-driven UI updates ────────────────────────

type UIEventType int

const (
	EventAppend           UIEventType = iota // new message appended to ChatMsgs
	EventAssistantChunk                      // streaming assistant text updated
	EventToolResultUpdate                    // tool result received (by ToolID)
	EventStreamDone                          // streaming finalized
	EventAgentUpdate                         // agent panel data changed
	EventReasoning                           // reasoning chunk (accumulate, don't add as chat message)
)

type UIEvent struct {
	Type    UIEventType
	Msg     ChatMessage // for EventAppend
	Text    string      // full accumulated streaming text for EventAssistantChunk
	ToolID  string      // for EventToolResultUpdate
	Result  string      // for EventToolResultUpdate
	IsError bool        // for EventToolResultUpdate
	AgentID string      // for EventAgentUpdate
}

// UIState holds bindings for cross-goroutine UI updates.
// Goroutines write to bindings; widgets read from them automatically.
type UIState struct {
	mu sync.RWMutex

	StatusText binding.String
	ModelName  binding.String
	TokenUsage binding.String
	ContextWin binding.String
	TokenPct   binding.Float

	AgentWorking atomic.Bool // true while agent is busy

	// Event callback: ChatView registers this to receive precise UI events.
	OnEvent func(UIEvent)

	// Assistant streaming buffer.
	assistantBuf strings.Builder

	// Non-bound state: chat messages.
	ChatMsgs []ChatMessage
	ChatMu   sync.Mutex

	// Agent panel state (sub-agents + teammates).
	agentMu     sync.Mutex
	agentPanels map[string]AgentPanelData
	agentDirty  bool

	// Status bar label reference (set once during buildUI).
	statusLabel *widget.Label

	// Streaming throttle: avoid per-token GUI redraws.
	streamLastNotify atomic.Int64 // unix millis of last EventAssistantChunk
	streamDirty      atomic.Bool  // true if buffered text not yet pushed to GUI
}

func (u *UIState) notify(e UIEvent) {
	if u.OnEvent != nil {
		u.OnEvent(e)
	}
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

// SetStatus updates the status bar binding. Safe from any goroutine.
func (u *UIState) SetStatus(text string) {
	_ = u.StatusText.Set(text)
}

// SetStatusDirect updates the status label directly. Must be called on UI thread only.
func (u *UIState) SetStatusDirect(text string) {
	_ = u.StatusText.Set(text)
	if u.statusLabel != nil {
		u.statusLabel.SetText(text)
	}
}

// SetStatusLabel stores a reference to the status bar label for direct updates.
func (u *UIState) SetStatusLabel(lbl *widget.Label) {
	u.statusLabel = lbl
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
// Fires EventAppend so the UI can render precisely.
func (u *UIState) AppendChat(msg ChatMessage) {
	u.ChatMu.Lock()

	// Merge consecutive system messages (e.g. repeated auto-compress notices).
	if msg.Role == "system" && len(u.ChatMsgs) > 0 {
		last := &u.ChatMsgs[len(u.ChatMsgs)-1]
		if last.Role == "system" {
			last.Content = msg.Content
			last.Time = msg.Time
			u.ChatMu.Unlock()
			u.notify(UIEvent{Type: EventAppend, Msg: msg})
			return
		}
	}

	u.ChatMsgs = append(u.ChatMsgs, msg)
	u.ChatMu.Unlock()
	u.notify(UIEvent{Type: EventAppend, Msg: msg})
}

// AppendReasoning sends a reasoning chunk to the UI without adding it as a chat message.
func (u *UIState) AppendReasoning(text string) {
	u.notify(UIEvent{Type: EventReasoning, Text: text})
}

// AppendAssistantText appends a streaming text chunk to the assistant buffer
// and updates (or creates) the last assistant message with the full accumulated text.
// Throttled: GUI updates at most every 80ms to avoid overwhelming Fyne's renderer.
func (u *UIState) AppendAssistantText(chunk string) {
	u.ChatMu.Lock()
	u.assistantBuf.WriteString(chunk)
	full := u.assistantBuf.String()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "assistant" && u.ChatMsgs[i].Streaming {
			u.ChatMsgs[i].Content = full
			u.ChatMu.Unlock()
			u.maybeNotifyChunk(full)
			return
		}
	}
	// No streaming assistant message yet, create one.
	msg := ChatMessage{
		Role:      "assistant",
		Content:   full,
		Streaming: true,
	}
	u.ChatMsgs = append(u.ChatMsgs, msg)
	u.ChatMu.Unlock()
	// First chunk: always notify immediately to create the widget.
	u.notify(UIEvent{Type: EventAppend, Msg: msg})
	u.notify(UIEvent{Type: EventAssistantChunk, Text: full})
	u.streamLastNotify.Store(time.Now().UnixMilli())
}

const streamThrottleMs = 80

func (u *UIState) maybeNotifyChunk(full string) {
	now := time.Now().UnixMilli()
	last := u.streamLastNotify.Load()
	if now-last >= streamThrottleMs {
		u.streamLastNotify.Store(now)
		u.streamDirty.Store(false)
		u.notify(UIEvent{Type: EventAssistantChunk, Text: full})
	} else {
		u.streamDirty.Store(true)
	}
}

// FlushStream forces an immediate EventAssistantChunk if dirty.
func (u *UIState) FlushStream() {
	if u.streamDirty.CompareAndSwap(true, false) {
		u.ChatMu.Lock()
		full := u.assistantBuf.String()
		u.ChatMu.Unlock()
		u.streamLastNotify.Store(time.Now().UnixMilli())
		u.notify(UIEvent{Type: EventAssistantChunk, Text: full})
	}
}

// UpdateToolResult updates the tool message with matching tool call ID.
func (u *UIState) UpdateToolResult(toolID, result string, isError bool) {
	u.ChatMu.Lock()
	for i := len(u.ChatMsgs) - 1; i >= 0; i-- {
		if u.ChatMsgs[i].Role == "tool" && u.ChatMsgs[i].ToolID == toolID {
			u.ChatMsgs[i].Content = result
			u.ChatMsgs[i].IsError = isError
			u.ChatMu.Unlock()
			u.notify(UIEvent{Type: EventToolResultUpdate, ToolID: toolID, Result: result, IsError: isError})
			return
		}
	}
	u.ChatMu.Unlock()
}

// FinalizeStreaming marks the last streaming assistant message as done,
// resets the streaming buffer, and marks any still-running tool calls as cancelled.
func (u *UIState) FinalizeStreaming() {
	u.FlushStream() // ensure last chunk is rendered before finalize
	u.ChatMu.Lock()
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
	u.ChatMu.Unlock()
	u.notify(UIEvent{Type: EventStreamDone})
}

// IsDirty returns whether the chat has pending updates (kept for compatibility).
func (u *UIState) IsDirty() bool { return false }

// TakeMessages returns a snapshot of all messages (kept for compatibility).
func (u *UIState) TakeMessages() []ChatMessage {
	u.ChatMu.Lock()
	defer u.ChatMu.Unlock()
	out := make([]ChatMessage, len(u.ChatMsgs))
	copy(out, u.ChatMsgs)
	return out
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
	if u.agentPanels == nil {
		u.agentPanels = make(map[string]AgentPanelData)
	}
	u.agentPanels[id] = data
	u.agentDirty = true
	u.agentMu.Unlock()
	u.notify(UIEvent{Type: EventAgentUpdate, AgentID: id})
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

// RemoveStalePanels removes completed/failed/idle panels older than 5 seconds.
func (u *UIState) RemoveStalePanels() bool {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	changed := false
	for id, p := range u.agentPanels {
		if p.Status == "completed" || p.Status == "failed" || p.Status == "idle" {
			if !p.CompletedAt.IsZero() && time.Since(p.CompletedAt) > 5*time.Second {
				delete(u.agentPanels, id)
				changed = true
			}
		}
	}
	if changed {
		u.agentDirty = true
	}
	return changed
}

// ClearAllAgentPanels removes every agent/teammate panel immediately.
// Used as a fallback when the main agent loop finishes to ensure no stale
// sub-agent or teammate tabs remain.
func (u *UIState) ClearAllAgentPanels() {
	u.agentMu.Lock()
	defer u.agentMu.Unlock()
	if len(u.agentPanels) > 0 {
		u.agentPanels = make(map[string]AgentPanelData)
		u.agentDirty = true
	}
}
