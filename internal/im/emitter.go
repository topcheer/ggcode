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

type IMEmitter struct {
	state    *imEmitterState
	typing   *imTypingKeeper
	manager  *Manager
	language string
	workDir  string

	mu         sync.Mutex
	lastStatus string
	outputMode string
}

type imEmitterState struct {
	mu      sync.Mutex
	started bool
	closed  bool
	ch      chan queuedIMEvent
}

type queuedIMEvent struct {
	mgr            *Manager
	event          OutboundEvent
	excludeAdapter string
}

func newIMEmitterState() *imEmitterState {
	return &imEmitterState{ch: make(chan queuedIMEvent, 256)}
}

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

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {

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

type imTypingKeeper struct {
	lastTrigger time.Time
	interval    time.Duration
}

const imTypingKeepaliveInterval = 5 * time.Second

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

func (e *IMEmitter) EmitEvent(event OutboundEvent) {
	if e == nil || e.manager == nil {
		return
	}
	if event.Kind == OutboundEventText {
		if strings.TrimSpace(event.Text) == "" {
			return
		}

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

	event.Text = e.relativizePaths(event.Text)
	event.Status = e.relativizePaths(event.Status)

	event.Text = redactResult(event.Text)
	event.Status = redactResult(event.Status)
	if event.ToolRes != nil {
		event.ToolRes.Args = e.relativizePaths(event.ToolRes.Args)
		event.ToolRes.Result = e.relativizePaths(event.ToolRes.Result)

		event.ToolRes.Args = redactResult(event.ToolRes.Args)
		event.ToolRes.Result = redactResult(event.ToolRes.Result)
	}

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

func (e *IMEmitter) redactOutbound(text string) string {
	if e == nil || text == "" {
		return text
	}
	return redactResult(e.relativizePaths(text))
}

func (e *IMEmitter) HasTargets() bool {
	if e == nil || e.manager == nil {
		return false
	}
	return e.manager.HasActiveBindings()
}

func (e *IMEmitter) Manager() *Manager {
	if e == nil {
		return nil
	}
	return e.manager
}

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

	cardText := e.redactOutbound(q.Title)
	if q.Kind == toolpkg.AskUserKindMulti {
		cardText += "\n📋 Multi-select — click options then ✅ Done"
	} else {
		cardText += "\n📋 Single-select — click one option"
	}
	if q.Prompt != "" && q.Prompt != q.Title {
		cardText += "\n" + e.redactOutbound(q.Prompt)
	}

	imMsg := InteractiveMessage{
		ID:          fmt.Sprintf("ask_%s_%d", q.ID, time.Now().UnixMilli()),
		Text:        cardText,
		Buttons:     buttons,
		MultiSelect: q.Kind == toolpkg.AskUserKindMulti,
		Placeholder: "Select an option",
	}

	fallbackRedacted := e.redactOutbound(fallbackText)
	msgIDs := e.manager.SendInteractive(context.Background(), imMsg)
	debug.Log("emitter", "EmitAskUserInteractive: SendInteractive returned msgIDs=%v", msgIDs)

	if strings.TrimSpace(fallbackRedacted) != "" {
		e.manager.EmitToNonInteractive(context.Background(), OutboundEvent{
			Kind: OutboundEventText,
			Text: fallbackRedacted,
		})
	}
	return msgIDs
}

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

func (e *IMEmitter) EmitUserText(text string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = e.EmitText(e.userEchoLabel() + text + "\n")
}

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

	echoText = e.redactOutbound(echoText)
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

func IsLongRunningTool(name string) bool {
	return imLongRunningTools[name]
}

func (e *IMEmitter) EmitToolStatus(toolName, rawArgs string) {
	if e == nil {
		return
	}
	if !imLongRunningTools[toolName] {
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

func (e *IMEmitter) EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {
	if e == nil {
		return
	}
	_, _, _ = toolCalls, toolSuccesses, toolFailures
	_ = e.EmitText(text)
}

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

func (e *IMEmitter) Language() string {
	if e == nil {
		return "en"
	}
	return e.language
}

func (e *IMEmitter) SetOutputMode(mode string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.outputMode = mode
	e.mu.Unlock()
}

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

func (e *IMEmitter) relativizePaths(text string) string {
	return util.RelativizePaths(text, e.workDir)
}

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
