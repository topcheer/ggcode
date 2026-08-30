package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// IMEmitter handles asynchronous outbound IM event emission with typing keepalive.
// It is framework-agnostic and can be used by both TUI and daemon modes.
type IMEmitter struct {
	state    *imEmitterState
	typing   *imTypingKeeper
	manager  *Manager
	language string // "zh-CN" or "en"
	workDir  string // project working directory for path relativization

	// mu protects lastStatus, outputMode, state, and typing from concurrent
	// access. These fields are read from the agent stream callback goroutine and
	// written from the TUI event loop or IM slash-command handler goroutine.
	// state and typing are lazily initialized; without the lock, two concurrent
	// emit paths could each install their own state (leaking a dispatcher
	// goroutine) and TriggerTyping raced on typing.lastTrigger (#954).
	mu         sync.Mutex
	lastStatus string // dedup status messages
	outputMode string // verbose, quiet, summary (default: verbose)
}

// imEmitterState manages a goroutine-based async event emission pipeline.
type imEmitterState struct {
	// mu guards ch, started, and closed. The dispatcher goroutine is started
	// lazily by the first enqueue; close() closes ch so the dispatcher drains
	// its buffer and exits. Previously close() shared the dispatcher-start
	// sync.Once, so after the first enqueue consumed the Once, close(ch) never
	// ran and the shutdown API was dead-on-arrival (#603).
	mu      sync.Mutex
	started bool
	closed  bool
	ch      chan queuedIMEvent
}

type queuedIMEvent struct {
	mgr            *Manager
	event          OutboundEvent
	excludeAdapter string // if set, skip this adapter when emitting
}

func newIMEmitterState() *imEmitterState {
	return &imEmitterState{ch: make(chan queuedIMEvent, 256)}
}

// close shuts down the dispatcher goroutine by closing the channel. Any
// buffered events are drained by the dispatcher before it exits. After close,
// enqueue calls become no-ops. Safe to call multiple times and safe to call
// after enqueue (#603: previously this was a silent no-op once enqueue had
// consumed the shared Once, so the dispatcher could never be shut down).
func (s *imEmitterState) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (s *imEmitterState) enqueue(mgr *Manager, event OutboundEvent, excludeAdapter string) {
	if s == nil || mgr == nil {
		return
	}
	// Hold mu across the send: close() also runs under mu, so a concurrent
	// close can never turn this send into a panic on a closed channel (#603).
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		// Dispatcher already shut down — drop the event.
		return
	}
	if !s.started {
		s.started = true
		safego.Go("im.emitter.dispatch", func() {
			for item := range s.ch {
				var err error
				if item.excludeAdapter != "" {
					err = item.mgr.EmitExcept(context.Background(), item.event, item.excludeAdapter)
				} else {
					err = item.mgr.Emit(context.Background(), item.event)
				}
				if err != nil && !errors.Is(err, ErrNoChannelBound) {
					debug.Log("emitter", "emit im kind=%s failed: %v", item.event.Kind, err)
				}
			}
		})
	}
	select {
	case s.ch <- queuedIMEvent{mgr: mgr, event: event, excludeAdapter: excludeAdapter}:
	default:
		debug.Log("emitter", "drop im event kind=%s: buffer full (len=%d)", event.Kind, len(s.ch))
	}
}

// imTypingKeeper tracks the last typing trigger time to implement keepalive.
type imTypingKeeper struct {
	lastTrigger time.Time
	interval    time.Duration
}

const imTypingKeepaliveInterval = 5 * time.Second

// NewIMEmitter creates a new IM emitter for the given manager, language, and working directory.
func NewIMEmitter(mgr *Manager, lang, workDir string) *IMEmitter {
	if mgr != nil {
		mgr.SetLanguage(lang)
	}
	return &IMEmitter{
		manager:  mgr,
		language: lang,
		workDir:  workDir,
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
		// Debug: log text emission with caller stack for tracing replay bugs
		if len(event.Text) > 50 {
			_, file, line, _ := runtime.Caller(2)
			debug.Log("emitter", "EmitText len=%d caller=%s:%d", len(event.Text), file, line)
		}
	}
	if event.Kind == OutboundEventStatus {
		event.Status = strings.TrimSpace(event.Status)
		if event.Status == "" {
			return
		}
	}

	// Relativize absolute paths in all output text
	event.Text = e.relativizePaths(event.Text)
	event.Status = e.relativizePaths(event.Status)
	// #1299: agent final replies carried secrets in cleartext to IM -
	// tool results were redacted (redactResult) but OutboundEventText
	// went straight to adapters, and IM is an outbound boundary (leaves
	// this machine for Telegram/Discord/DingTalk servers). Redact at
	// this single choke point so every emitter path is covered.
	event.Text = redactResult(event.Text)
	event.Status = redactResult(event.Status)
	if event.ToolRes != nil {
		event.ToolRes.Args = e.relativizePaths(event.ToolRes.Args)
		event.ToolRes.Result = e.relativizePaths(event.ToolRes.Result)
	}

	// Set language on tool events so format functions can localize
	if event.ToolCall != nil && event.ToolCall.Lang == "" {
		event.ToolCall.Lang = e.language
	}
	if event.ToolRes != nil && event.ToolRes.Lang == "" {
		event.ToolRes.Lang = e.language
	}

	switch event.Kind {
	case OutboundEventText:
		debug.Log("emitter", "emit im text len=%d", len(event.Text))
	case OutboundEventStatus:
		debug.Log("emitter", "emit im status=%q", truncateEmitter(event.Status, 80))
	default:
		// Don't log every emit — extremely noisy for tool_result events
	}
	e.mu.Lock()
	if e.state == nil {
		e.state = newIMEmitterState()
	}
	st := e.state
	e.mu.Unlock()
	st.enqueue(e.manager, event, "")
	e.TriggerTyping()
}

// HasTargets returns true if at least one IM channel is bound.
// Uses a lightweight check that avoids copying the bindings list.
func (e *IMEmitter) HasTargets() bool {
	if e == nil || e.manager == nil {
		return false
	}
	return e.manager.HasActiveBindings()
}

// Manager returns the underlying Manager. Returns nil if the emitter is nil.
func (e *IMEmitter) Manager() *Manager {
	if e == nil {
		return nil
	}
	return e.manager
}

// EmitAskUserInteractive sends an ask_user question to IM, preferring
// interactive buttons for adapters that support them (Discord, Telegram,
// Feishu). Text fallback is only sent to adapters that did NOT receive an
// interactive message (e.g. QQ, DingDing).
// If the question has no choices, falls back to EmitAskUser for all adapters.
func (e *IMEmitter) EmitAskUserInteractive(title string, q toolpkg.AskUserQuestion, fallbackText string) map[string]string {
	if e == nil || e.manager == nil {
		return nil
	}
	if len(q.Choices) == 0 {
		e.EmitAskUser(fallbackText)
		return nil
	}

	buttons := make([]InteractiveButton, len(q.Choices))
	for ci, choice := range q.Choices {
		buttons[ci] = InteractiveButton{
			Label: choice.Label,
			Value: fmt.Sprintf("%d", ci+1),
			Style: "default",
		}
	}
	if len(q.Choices) == 2 {
		buttons[0].Style = "primary"
	}

	cardText := q.Title
	if q.Kind == toolpkg.AskUserKindMulti {
		cardText += "\n📋 Multi-select — click options then ✅ Done"
	} else {
		cardText += "\n📋 Single-select — click one option"
	}
	if q.Prompt != "" && q.Prompt != q.Title {
		cardText += "\n" + q.Prompt
	}

	imMsg := InteractiveMessage{
		ID:          fmt.Sprintf("ask_%s_%d", q.ID, time.Now().UnixMilli()),
		Text:        cardText,
		Buttons:     buttons,
		MultiSelect: q.Kind == toolpkg.AskUserKindMulti,
		Placeholder: "Select an option",
	}

	msgIDs := e.manager.SendInteractive(context.Background(), imMsg)
	debug.Log("emitter", "EmitAskUserInteractive: SendInteractive returned msgIDs=%v", msgIDs)
	// Send text fallback ONLY to adapters that do NOT support InteractiveSender
	if strings.TrimSpace(fallbackText) != "" {
		e.manager.EmitToNonInteractive(context.Background(), OutboundEvent{
			Kind: OutboundEventText,
			Text: fallbackText,
		})
	}
	return msgIDs
}

// EmitText sends a text message to IM. Returns error if emission fails (e.g., IM disconnected).
func (e *IMEmitter) EmitText(text string) error {
	if e == nil {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	e.mu.Lock()
	e.lastStatus = ""
	e.mu.Unlock()
	e.EmitEvent(OutboundEvent{
		Kind: OutboundEventText,
		Text: text,
	})
	return nil
}

// EmitUserText sends a user echo message to IM.
func (e *IMEmitter) EmitUserText(text string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = e.EmitText(e.userEchoLabel() + text + "\n")
}

// EmitUserTextExcept sends a user echo message to all bound IM channels except the originating adapter.
// This prevents the user from seeing their own message echoed back on the channel they sent from.
func (e *IMEmitter) EmitUserTextExcept(text, excludeAdapter string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	echoText := e.userEchoLabel() + text + "\n"
	if strings.TrimSpace(echoText) == "" {
		return
	}
	e.mu.Lock()
	e.lastStatus = ""
	if e.state == nil {
		e.state = newIMEmitterState()
	}
	st := e.state
	e.mu.Unlock()
	st.enqueue(e.manager, OutboundEvent{
		Kind: OutboundEventText,
		Text: echoText,
	}, excludeAdapter)
	e.TriggerTyping()
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
	e.mu.Lock()
	if status == e.lastStatus {
		e.mu.Unlock()
		return
	}
	e.lastStatus = status
	e.mu.Unlock()
	e.EmitEvent(OutboundEvent{
		Kind:   OutboundEventStatus,
		Status: status,
	})
}

// imLongRunningTools lists tools that get a start notification via EmitToolStatus.
// Other tools only get a result notification to avoid duplicate IM messages.
var imLongRunningTools = map[string]bool{
	"run_command":      true,
	"bash":             true,
	"start_command":    true,
	"powershell":       true,
	"web_fetch":        true,
	"web_search":       true,
	"browser":          true,
	"spawn_agent":      true,
	"edit_file":        true,
	"write_file":       true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"sleep":            true,
	"delegate":         true,
	"a2a_remote":       true,
	"a2a_send_task":    true,
	"skill":            true,
}

// IsLongRunningTool reports whether the tool gets a start notification.
func IsLongRunningTool(name string) bool {
	return imLongRunningTools[name]
}

// EmitToolStatus formats and sends a tool execution status using the shared
// DescribeTool pipeline. Only long-running tools get a start notification
// to avoid duplicate IM messages (IM does not support message merging).
func (e *IMEmitter) EmitToolStatus(toolName, rawArgs string) {
	if e == nil {
		return
	}
	if !imLongRunningTools[toolName] {
		return // short tools: only result notification, no start notification
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
	_ = e.EmitText(text)
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
	_ = e.EmitText(text)
}

// TriggerTyping sends typing indicators to all bound adapters with keepalive throttling.
func (e *IMEmitter) TriggerTyping() {
	if e == nil || e.manager == nil {
		return
	}
	now := time.Now()
	e.mu.Lock()
	if e.typing == nil {
		e.typing = &imTypingKeeper{interval: imTypingKeepaliveInterval}
	}
	if now.Sub(e.typing.lastTrigger) < e.typing.interval {
		e.mu.Unlock()
		return
	}
	e.typing.lastTrigger = now
	e.mu.Unlock()
	safego.Go("im.emitter.typing", func() {
		e.manager.TriggerTyping(context.Background())
	})
}

// Close shuts down the emitter's dispatcher goroutine, draining any buffered
// events. Previously the state-level close() existed but nothing called it,
// so the dispatcher goroutine leaked for the process lifetime (#603, #954).
// After Close, further emissions re-initialize a fresh state lazily.
func (e *IMEmitter) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	st := e.state
	e.state = nil
	e.mu.Unlock()
	if st != nil {
		st.close()
	}
}

// Language returns the emitter's configured language.
func (e *IMEmitter) Language() string {
	if e == nil {
		return "en"
	}
	return e.language
}

// SetOutputMode sets the IM output mode: verbose, quiet, or summary.
func (e *IMEmitter) SetOutputMode(mode string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.outputMode = mode
	e.mu.Unlock()
}

// OutputMode returns the current output mode.
func (e *IMEmitter) OutputMode() string {
	if e == nil {
		return "verbose"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.outputMode == "" {
		return "verbose"
	}
	return e.outputMode
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

// userEchoLabel returns the localized prefix for user echo messages.
func (e *IMEmitter) userEchoLabel() string {
	if e.language == "zh-CN" {
		return "【用户】"
	}
	return "[User] "
}

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

// relativizePaths replaces absolute paths under workDir with relative paths (./).
func (e *IMEmitter) relativizePaths(text string) string {
	return util.RelativizePaths(text, e.workDir)
}

// EmitKnightReport sends a Knight status report to IM.
func (e *IMEmitter) EmitKnightReport(report string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(report) == "" {
		return
	}
	e.EmitEvent(OutboundEvent{
		Kind: OutboundEventText,
		Text: "🌙 " + report,
	})
}
