package im

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// IMEmitter handles asynchronous outbound IM event emission with typing keepalive.
// It is framework-agnostic and can be used by both TUI and daemon modes.
type IMEmitter struct {
	state      *imEmitterState
	typing     *imTypingKeeper
	manager    *Manager
	language   string // "zh-CN" or "en"
	lastStatus string // dedup status messages
}

// imEmitterState manages a goroutine-based async event emission pipeline.
type imEmitterState struct {
	once sync.Once
	ch   chan queuedIMEvent
}

type queuedIMEvent struct {
	mgr   *Manager
	event OutboundEvent
}

func newIMEmitterState() *imEmitterState {
	return &imEmitterState{ch: make(chan queuedIMEvent, 256)}
}

func (s *imEmitterState) enqueue(mgr *Manager, event OutboundEvent) {
	if s == nil || mgr == nil {
		return
	}
	s.once.Do(func() {
		go func() {
			for item := range s.ch {
				err := item.mgr.Emit(context.Background(), item.event)
				if err != nil && !errors.Is(err, ErrNoChannelBound) {
					debug.Log("emitter", "emit im kind=%s failed: %v", item.event.Kind, err)
				}
			}
		}()
	})
	s.ch <- queuedIMEvent{mgr: mgr, event: event}
}

// imTypingKeeper tracks the last typing trigger time to implement keepalive.
type imTypingKeeper struct {
	lastTrigger time.Time
	interval    time.Duration
}

const imTypingKeepaliveInterval = 5 * time.Second

// NewIMEmitter creates a new IM emitter for the given manager and language.
func NewIMEmitter(mgr *Manager, lang string) *IMEmitter {
	return &IMEmitter{
		manager:  mgr,
		language: lang,
	}
}

// EmitEvent sends a raw outbound event to all bound IM channels.
func (e *IMEmitter) EmitEvent(event OutboundEvent) {
	if e == nil || e.manager == nil {
		return
	}
	if event.Kind == OutboundEventText {
		if strings.TrimSpace(event.Text) == "" {
			return
		}
	}
	if event.Kind == OutboundEventStatus {
		event.Status = strings.TrimSpace(event.Status)
		if event.Status == "" {
			return
		}
	}
	switch event.Kind {
	case OutboundEventText:
		debug.Log("emitter", "emit im text len=%d", len(event.Text))
	case OutboundEventStatus:
		debug.Log("emitter", "emit im status=%q", truncateEmitter(event.Status, 80))
	default:
		debug.Log("emitter", "emit im kind=%s", event.Kind)
	}
	if e.state == nil {
		e.state = newIMEmitterState()
	}
	e.state.enqueue(e.manager, event)
	e.TriggerTyping()
}

// EmitText sends a text message to IM.
func (e *IMEmitter) EmitText(text string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	e.lastStatus = ""
	e.EmitEvent(OutboundEvent{
		Kind: OutboundEventText,
		Text: text,
	})
}

// EmitUserText sends a user echo message to IM.
func (e *IMEmitter) EmitUserText(text string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	e.EmitText("【用户】" + text + "\n")
}

// EmitStatus sends a status update to IM. Duplicate consecutive statuses are suppressed.
func (e *IMEmitter) EmitStatus(status string) {
	if e == nil {
		return
	}
	status = strings.TrimSpace(status)
	if status == "" {
		return
	}
	if status == e.lastStatus {
		return
	}
	e.lastStatus = status
	e.EmitEvent(OutboundEvent{
		Kind:   OutboundEventStatus,
		Status: status,
	})
}

// EmitToolStatus formats and sends a tool execution status using the shared
// DescribeTool pipeline.
func (e *IMEmitter) EmitToolStatus(toolName, rawArgs string) {
	if e == nil {
		return
	}
	lang := ToolLanguage(e.language)
	present := DescribeTool(lang, toolName, rawArgs)
	summary := strings.TrimSpace(firstNonEmptyStr(present.Activity, present.DisplayName))
	if summary == "" {
		return
	}
	status := LocalizeIMProgress(lang, summary)
	e.EmitStatus(status)
}

// EmitRoundSummary sends the final round text to IM.
func (e *IMEmitter) EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {
	if e == nil {
		return
	}
	_, _, _ = toolCalls, toolSuccesses, toolFailures
	e.EmitText(text)
}

// EmitAskUser sends an ask_user prompt to IM.
func (e *IMEmitter) EmitAskUser(text string) {
	if e == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	e.EmitText(text)
}

// TriggerTyping sends typing indicators to all bound adapters with keepalive throttling.
func (e *IMEmitter) TriggerTyping() {
	if e == nil || e.manager == nil {
		return
	}
	now := time.Now()
	if e.typing == nil {
		e.typing = &imTypingKeeper{interval: imTypingKeepaliveInterval}
	}
	if now.Sub(e.typing.lastTrigger) < e.typing.interval {
		return
	}
	e.typing.lastTrigger = now
	go e.manager.TriggerTyping(context.Background())
}

// Language returns the emitter's configured language.
func (e *IMEmitter) Language() string {
	if e == nil {
		return "en"
	}
	return e.language
}

// FormatAskUserPrompt formats an ask_user request as an IM-friendly prompt string.
// Delegates to the shared FormatAskUserPrompt in ask_user_format.go.
func (e *IMEmitter) FormatAskUserPrompt(rawArgs string) string {
	if e == nil {
		return ""
	}
	rawArgs = strings.TrimSpace(rawArgs)
	if rawArgs == "" {
		return ""
	}
	var req toolpkg.AskUserRequest
	if err := json.Unmarshal([]byte(rawArgs), &req); err != nil {
		target := strings.TrimSpace(extractAskUserTarget(rawArgs))
		if target == "" {
			return ""
		}
		switch e.language {
		case "zh-CN":
			return "我需要你补充信息：\n" + target
		default:
			return "I need a bit more input:\n" + target
		}
	}

	return FormatAskUserPrompt(e.language, req)
}

// Helper functions

func truncateEmitter(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func extractAskUserTarget(rawArgs string) string {
	args := parseToolArgs(rawArgs)
	if args == nil {
		return ""
	}
	if title := strings.TrimSpace(argString(args, "title")); title != "" {
		return title
	}
	return ""
}
