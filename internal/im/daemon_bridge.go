package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// pendingAskUser tracks an in-flight ask_user request waiting for an IM reply.
type pendingAskUser struct {
	request     toolpkg.AskUserRequest
	response    chan toolpkg.AskUserResponse
	multiSelect bool // if true, accumulate selections until __done__
}

// DaemonBridge implements the Bridge interface for headless (daemon) mode.
// IM inbound messages are submitted directly to the agent without a TUI.
// pendingInterruption wraps a queued user message with all its content blocks.
type pendingInterruption struct {
	Content []provider.ContentBlock
}

type DaemonBridge struct {
	manager         *Manager
	emitter         *IMEmitter
	agent           *agent.Agent
	store           session.Store
	sess            *session.Session
	language        string
	harnessMode     string // "off", "suggest", "on", "strict"
	harnessAutoInit bool
	workingDir      string
	usageTurnIndex  int
	metricCollector *metrics.Collector
	metricCancel    context.CancelFunc

	mu                   sync.Mutex
	cancelFunc           context.CancelFunc
	pendingAsk           *pendingAskUser
	pendingApproval      chan permission.Decision // non-nil when waiting for IM approval reply
	pendingInterruptions []pendingInterruption
	interactiveMsgIDs    map[string]string // adapter → platform msg ID (for callback correlation)
	multiSelectChosen    map[string]bool   // accumulated multi-select choices (choice value → selected)
	followSink           daemon.FollowSink
	onActivity           func()
	onRunStateChange     func(bool)
	onUserMessage        func([]provider.ContentBlock)
	onRestart            func()                                               // trigger daemon self-restart
	onProviderSwitch     func(vendor, endpoint, model string) (string, error) // switch provider/model, returns summary
	restartDebug         bool                                                 // set by /restart debug to enable debug logging on next launch
	eventSubs            []*daemonBridgeSub
	eventSubMu           sync.RWMutex
}

// NewDaemonBridge creates a bridge that submits IM messages directly to the agent.
func NewDaemonBridge(mgr *Manager, ag *agent.Agent, emitter *IMEmitter, store session.Store, sess *session.Session) *DaemonBridge {
	b := &DaemonBridge{
		manager:  mgr,
		agent:    ag,
		emitter:  emitter,
		store:    store,
		sess:     sess,
		language: emitter.Language(),
	}
	if sess != nil {
		b.usageTurnIndex = daemonSessionTurnIndex(sess)
	}
	// Register interactive callback handler so button clicks from adapters
	// are routed to the pending ask_user question.
	if mgr != nil {
		mgr.SetInteractiveCallback(b.handleInteractiveCallback)
	}
	// Register approval handler so tool permission requests are pushed to IM.
	if ag != nil {
		ag.SetApprovalHandler(b.handleApproval)
		if store != nil && sess != nil {
			collectorCtx, collectorCancel := context.WithCancel(context.Background())
			b.metricCancel = collectorCancel
			b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
				b.recordMetric(ev)
			})
			ag.SetMetricHandler(b.metricCollector.Emit)
		}
	}
	return b
}

// SetHarnessConfig configures auto-run routing for daemon mode.
func (b *DaemonBridge) SetHarnessConfig(mode string, autoInit bool, workingDir string) {
	b.harnessMode = mode
	b.harnessAutoInit = autoInit
	b.workingDir = workingDir
}

// It translates the selected values into a text reply and feeds it through
// the same pendingAsk mechanism as text replies.
func (b *DaemonBridge) handleInteractiveCallback(cb InteractiveCallback) {
	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if pending == nil {
		return
	}

	// Determine the choice value from the callback.
	// For multi-select, each click toggles a selection; __done__ submits.
	choice := ""
	if len(cb.Values) > 0 {
		choice = cb.Values[0]
	}

	if pending.multiSelect && choice != "__done__" {
		// Toggle the selection
		b.mu.Lock()
		if b.multiSelectChosen == nil {
			b.multiSelectChosen = make(map[string]bool)
		}
		if b.multiSelectChosen[choice] {
			delete(b.multiSelectChosen, choice)
		} else {
			b.multiSelectChosen[choice] = true
		}
		chosen := b.multiSelectChosen
		b.mu.Unlock()
		debug.Log("im", "multi-select toggle: choice=%s selected=%v", choice, chosen)
		return
	}

	// Submit: single-select sends immediately; multi-select sends accumulated on Done
	var values []string
	if pending.multiSelect {
		// __done__ was clicked — collect all accumulated selections
		b.mu.Lock()
		for v := range b.multiSelectChosen {
			values = append(values, v)
		}
		b.multiSelectChosen = nil
		b.mu.Unlock()
	} else {
		values = cb.Values
	}

	if len(values) == 0 {
		return
	}

	text := strings.Join(values, ",")
	resp := BuildAskUserResponseFromText(pending.request, text)
	select {
	case pending.response <- resp:
	default:
	}
}

// SetFollowSink sets or clears the follow-mode display sink.
func (b *DaemonBridge) SetFollowSink(sink daemon.FollowSink) {
	b.mu.Lock()
	b.followSink = sink
	b.mu.Unlock()
}

// SetActivityHook installs a callback fired when an inbound IM message counts as
// real user activity. Daemon mode uses this to keep Knight's idle timer honest.
func (b *DaemonBridge) SetActivityHook(fn func()) {
	b.mu.Lock()
	b.onActivity = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetRunStateHook(fn func(bool)) {
	b.mu.Lock()
	b.onRunStateChange = fn
	b.mu.Unlock()
}

func (b *DaemonBridge) SetUserMessageHook(fn func([]provider.ContentBlock)) {
	b.mu.Lock()
	b.onUserMessage = fn
	b.mu.Unlock()
}

// SetRestartHook installs a callback to trigger daemon process restart.
func (b *DaemonBridge) SetRestartHook(fn func()) {
	b.mu.Lock()
	b.onRestart = fn
	b.mu.Unlock()
}

// SetProviderSwitchHook installs a callback to switch provider/model.
// The callback receives (vendor, endpoint, model) — any may be empty to mean "keep current".
// It returns a human-readable summary string.
func (b *DaemonBridge) SetProviderSwitchHook(fn func(vendor, endpoint, model string) (string, error)) {
	b.mu.Lock()
	b.onProviderSwitch = fn
	b.mu.Unlock()
}

// ConsumeRestartDebug returns whether /restart debug was requested and resets the flag.
func (b *DaemonBridge) ConsumeRestartDebug() bool {
	b.mu.Lock()
	v := b.restartDebug
	b.restartDebug = false
	b.mu.Unlock()
	return v
}

func (b *DaemonBridge) HasActiveRun() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancelFunc != nil
}

func (b *DaemonBridge) InterruptActiveRun() bool {
	b.mu.Lock()
	cancel := b.cancelFunc
	b.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (b *DaemonBridge) notifyRunStateChange(busy bool) {
	b.mu.Lock()
	fn := b.onRunStateChange
	b.mu.Unlock()
	if fn != nil {
		fn(busy)
	}
}

func (b *DaemonBridge) notifyUserMessage(content []provider.ContentBlock) {
	b.mu.Lock()
	fn := b.onUserMessage
	b.mu.Unlock()
	if fn != nil {
		fn(content)
	}
}

func (b *DaemonBridge) tryQueueInterruption(content []provider.ContentBlock, logPrefix string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelFunc == nil {
		return false
	}
	debug.Log("daemon-bridge", "%squeuing interruption: %s", logPrefix, truncateStr(extractText(content), 80))
	b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{Content: content})
	return true
}

// tryQueueOrBeginRun atomically checks if the agent is busy and either
// queues the interruption or begins a new run slot. This eliminates the
// TOCTOU window between tryQueueInterruption and beginRunSlot.
// Returns (nil, true) if queued, (ctx, false) if a new run was begun.
func (b *DaemonBridge) tryQueueOrBeginRun(content []provider.ContentBlock, logPrefix string) (context.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancelFunc != nil {
		debug.Log("daemon-bridge", "%squeuing interruption: %s", logPrefix, truncateStr(extractText(content), 80))
		b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{Content: content})
		return nil, true
	}
	// Begin run slot (inline of beginRunSlot logic)
	ctx, cancel := context.WithCancel(context.Background())
	b.pendingInterruptions = b.pendingInterruptions[:0]
	b.agent.SetInterruptionHandler(func() string {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.pendingInterruptions) == 0 {
			return ""
		}
		next := b.pendingInterruptions[0]
		b.pendingInterruptions = b.pendingInterruptions[1:]
		debug.Log("daemon-bridge", "interruption handler returning: %s", truncateStr(extractText(next.Content), 80))
		return extractText(next.Content)
	})
	b.cancelFunc = cancel
	return ctx, false
}

func (b *DaemonBridge) beginRunSlot() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx2, cancel := context.WithCancel(context.Background())
	b.pendingInterruptions = b.pendingInterruptions[:0]
	b.agent.SetInterruptionHandler(func() string {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.pendingInterruptions) == 0 {
			return ""
		}
		msg := b.pendingInterruptions[0]
		b.pendingInterruptions = b.pendingInterruptions[1:]
		return extractText(msg.Content)
	})
	b.cancelFunc = cancel
	return ctx2
}

func (b *DaemonBridge) finishRunSlot() {
	b.mu.Lock()
	b.cancelFunc = nil
	b.agent.SetInterruptionHandler(nil)
	b.mu.Unlock()
	b.notifyRunStateChange(false)
}

func (b *DaemonBridge) nextPendingInterruption() []provider.ContentBlock {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pendingInterruptions) == 0 {
		return nil
	}
	next := b.pendingInterruptions[0].Content
	b.pendingInterruptions = b.pendingInterruptions[1:]
	return next
}

func (b *DaemonBridge) runQueuedLoop(ctx context.Context, content []provider.ContentBlock, logPrefix string, preRun func(context.Context, string) bool, onRunError func(error)) {
	defer b.finishRunSlot()
	for {
		text := extractText(content)
		if preRun != nil && preRun(ctx, text) {
			return
		}
		err := b.runAgentStream(ctx, content)
		if err != nil && !errors.Is(err, context.Canceled) && onRunError != nil {
			onRunError(err)
		}

		nextContent := b.nextPendingInterruption()
		if len(nextContent) == 0 {
			return
		}
		debug.Log("daemon-bridge", "%sdraining queued message: %s", logPrefix, truncateStr(extractText(nextContent), 80))
		content = nextContent
	}
}

// SubmitInboundMessage handles an inbound IM message by submitting it to the agent.
func (b *DaemonBridge) SubmitInboundMessage(ctx context.Context, msg InboundMessage) error {
	if b == nil {
		return fmt.Errorf("daemon bridge not initialized")
	}
	text := strings.TrimSpace(msg.Text)
	if text != "" {
		b.mu.Lock()
		onActivity := b.onActivity
		b.mu.Unlock()
		if onActivity != nil {
			onActivity()
		}
	}

	b.mu.Lock()
	hasPendingApproval := b.pendingApproval != nil
	hasPendingAsk := b.pendingAsk != nil
	b.mu.Unlock()
	route := RouteInboundText(text, hasPendingApproval, hasPendingAsk)

	// Slash commands take priority over everything (including pending approval/ask_user)
	if route.Kind == InboundRouteSlash {
		return b.handleSlashCommand(ctx, text, msg)
	}

	// Check for pending approval — y/a/n reply for tool permission
	b.mu.Lock()
	approvalCh := b.pendingApproval
	b.mu.Unlock()
	if route.Kind == InboundRouteApproval && approvalCh != nil {
		approvalCh <- route.Decision
		b.mu.Lock()
		b.pendingApproval = nil
		b.mu.Unlock()
		return nil
	}

	// Check for pending ask_user — if so, route reply there
	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if route.Kind == InboundRouteAskUser && pending != nil {
		resp := BuildAskUserResponseFromText(pending.request, route.Text)
		pending.response <- resp
		b.mu.Lock()
		b.pendingAsk = nil
		b.mu.Unlock()
		return nil
	}

	// Normal agent submission
	content := msg.ProviderContent()
	if len(content) == 0 {
		return nil
	}

	// Check text is not empty
	if route.Kind == InboundRouteEmpty || text == "" {
		return nil
	}
	b.notifyUserMessage(content)

	// Immediately trigger typing indicator so the user sees feedback
	// before waiting for the first LLM token.
	b.emitter.TriggerTyping()

	// Notify follow sink of user message
	b.mu.Lock()
	sink := b.followSink
	b.mu.Unlock()
	if sink != nil {
		sink.OnUserMessage(text)
	}

	content = []provider.ContentBlock{{Type: "text", Text: text}}
	ctx2, queued := b.tryQueueOrBeginRun(content, "")
	if queued {
		return nil
	}
	b.notifyRunStateChange(true)
	b.runQueuedLoop(ctx2, content, "", func(ctx context.Context, text string) bool {
		if text != "" && b.harnessMode != "" && b.harnessMode != "off" {
			return b.tryHarnessAutoRun(ctx, text) != nil
		}
		return false
	}, func(err error) {
		b.emitter.EmitText(provider.UserFacingError(err))
	})

	return nil
}

// HandleAskUser is the AskUserHandler for daemon mode — sends questions to IM
// one at a time and collects answers interactively.
// For single-choice questions, it first attempts to send native interactive
// buttons. If the adapter supports it, the user clicks a button instead of
// typing a number. The answer is routed back through the same pendingAsk
// mechanism — either from a text reply or a button callback.
func (b *DaemonBridge) HandleAskUser(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
	answers := make([]toolpkg.AskUserAnswer, len(req.Questions))
	answeredCount := 0

	for i, q := range req.Questions {
		// Format the question text (for display + fallback)
		singleReq := toolpkg.AskUserRequest{
			Title:     req.Title,
			Questions: []toolpkg.AskUserQuestion{q},
		}
		argsJSON, _ := jsonMarshalArgs(singleReq)
		text := b.emitter.FormatAskUserPrompt(argsJSON)
		if text == "" {
			text = strings.TrimSpace(q.Title)
		}

		// Try interactive buttons for choice questions; fallback to text for
		// adapters that don't support InteractiveSender (e.g. QQ, DingDing).
		if len(q.Choices) > 0 {
			msgIDs := b.emitter.EmitAskUserInteractive(q.Title, q, text)
			if len(msgIDs) > 0 {
				b.mu.Lock()
				b.interactiveMsgIDs = msgIDs
				b.mu.Unlock()
			}
		} else {
			// Text-only question — send plain text to all adapters
			if text != "" {
				b.emitter.EmitAskUser(text)
			}
		}

		// Block until the user replies via IM (text or button callback)
		isMulti := q.Kind == toolpkg.AskUserKindMulti
		pending := &pendingAskUser{
			request:     singleReq, // use single-question request for correct answer mapping
			response:    make(chan toolpkg.AskUserResponse, 1),
			multiSelect: isMulti,
		}
		b.mu.Lock()
		b.pendingAsk = pending
		if isMulti {
			b.multiSelectChosen = nil // reset accumulated selections
		}
		b.mu.Unlock()

		select {
		case resp := <-pending.response:
			if len(resp.Answers) > 0 && resp.Answers[0].Answered {
				answers[i] = resp.Answers[0]
				answeredCount++
			} else {
				answers[i] = toolpkg.AskUserAnswer{
					ID:               q.ID,
					Title:            q.Title,
					Kind:             q.Kind,
					CompletionStatus: toolpkg.AskUserCompletionUnanswered,
					AnswerMode:       toolpkg.AskUserAnswerModeNone,
					Answered:         false,
				}
			}
		case <-ctx.Done():
			b.mu.Lock()
			b.pendingAsk = nil
			b.mu.Unlock()
			return toolpkg.AskUserResponse{}, ctx.Err()
		}
	}

	return toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusSubmitted,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		AnsweredCount: answeredCount,
		Answers:       answers,
	}, nil
}

// handleApproval is the ApprovalHandler for daemon mode — pushes tool permission
// requests to IM and waits for a y/a/n reply.
func (b *DaemonBridge) handleApproval(ctx context.Context, toolName string, input string) permission.Decision {
	lang := b.language
	var prompt string
	if lang == "zh-CN" || lang == "zh" {
		prompt = FormatApprovalRequest(ToolLangZhCN, toolName, input)
	} else {
		prompt = FormatApprovalRequest(ToolLangEn, toolName, input)
	}
	b.emitter.EmitText(prompt)

	// Create a channel and register it so IM replies can resolve it.
	ch := make(chan permission.Decision, 1)
	b.mu.Lock()
	b.pendingApproval = ch
	b.mu.Unlock()

	debug.Log("daemon", "approval: waiting for IM reply on tool=%s", toolName)

	select {
	case decision := <-ch:
		debug.Log("daemon", "approval: received %v for tool=%s", decision, toolName)
		// Send result confirmation back to IM
		var resultMsg string
		decisionStr := "deny"
		if decision == permission.Allow {
			decisionStr = "allow"
		}
		if lang == "zh-CN" || lang == "zh" {
			resultMsg = FormatApprovalResult(ToolLangZhCN, toolName, decisionStr)
		} else {
			resultMsg = FormatApprovalResult(ToolLangEn, toolName, decisionStr)
		}
		if resultMsg != "" {
			b.emitter.EmitText(resultMsg)
		}
		return decision
	case <-ctx.Done():
		debug.Log("daemon", "approval: context cancelled for tool=%s", toolName)
		b.mu.Lock()
		b.pendingApproval = nil
		b.mu.Unlock()
		return permission.Deny
	}
}

// formatToolInline renders a concise one-line tool summary for approval prompts.
func formatToolInline(toolName, input string) string {
	if input == "" {
		return toolName
	}
	// Try to extract a short description from JSON input
	var m map[string]any
	if err := json.Unmarshal([]byte(input), &m); err == nil {
		for _, key := range []string{"command", "path", "file_path", "query", "url", "message"} {
			if v, ok := m[key].(string); ok && v != "" {
				return fmt.Sprintf("%s: %s", toolName, truncate(v, 60))
			}
		}
	}
	return fmt.Sprintf("%s: %s", toolName, truncate(input, 60))
}

// runAgentStream executes the agent with streaming output sent to IM.
// IM messages mirror TUI behavior exactly: text is only emitted on
// StreamEventDone (not per-token), tool status is emitted as it happens.
func (b *DaemonBridge) runAgentStream(ctx context.Context, content []provider.ContentBlock) error {
	round := &daemonRoundState{}
	b.mu.Lock()
	b.usageTurnIndex++
	sink := b.followSink
	b.mu.Unlock()

	// Save user message to session
	b.appendUserMessage(content)

	// Auto-run routing check: log suggestion for harness-eligible tasks.
	// In daemon mode, auto-run is informational only — the agent decides
	// whether to use harness based on its skill instructions.
	b.checkAutoRunSuggestion(extractText(content))

	err := b.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
		defer safego.Recover("im.daemonBridge.streamCallback")
		// Broadcast to webchat subscribers
		b.broadcastEvent(event)

		switch event.Type {
		case provider.StreamEventText:
			// Accumulate text, do NOT send to IM per-token
			round.AppendText(event.Text)
			b.emitter.TriggerTyping()
			if sink != nil {
				sink.OnStreamText(event.Text)
			}

		case provider.StreamEventToolCallDone:
			toolName := strings.TrimSpace(event.Tool.Name)
			if toolName == "ask_user" {
				round.SetAskUser(b.emitter.FormatAskUserPrompt(string(event.Tool.Arguments)))
			}
			if !isDaemonSkippedTool(toolName) {
				round.NoteToolCall()
			}
			// Sleep tool is special: emit the duration immediately
			// so the user sees it before the tool blocks.
			// Only emit in verbose mode — quiet/summary aggregate it.
			if toolName == "sleep" && b.resolveEffectiveOutputMode() == "verbose" {
				b.emitter.EmitEvent(OutboundEvent{
					Kind: OutboundEventToolCall,
					ToolCall: &ToolCallInfo{
						ToolName: "sleep",
						Args:     string(event.Tool.Arguments),
						Detail:   formatSleepDuration(string(event.Tool.Arguments)),
					},
				})
			}
			// Do NOT emit intermediate status to IM — only final results
			// via OutboundEventToolResult (mirrors terminal follow behavior).
			b.emitter.TriggerTyping()
			// Note: followSink does NOT get OnToolStatus — only final
			// OnToolResult is forwarded to keep terminal output clean.

		case provider.StreamEventToolResult:
			round.NoteToolResult(event.IsError)
			toolInfo := ToolResultInfo{
				ToolName: event.Tool.Name,
				Args:     string(event.Tool.Arguments),
				Result:   event.Result,
				IsError:  event.IsError,
				Lang:     b.language,
			}

			switch b.resolveEffectiveOutputMode() {
			case "summary":
				// Only buffer, never send individual tool results
				round.PendingTools = append(round.PendingTools, toolInfo)
			case "quiet":
				// Buffer for aggregation; errors still sent immediately
				if event.IsError {
					b.emitter.EmitEvent(OutboundEvent{
						Kind:    OutboundEventToolResult,
						ToolRes: &toolInfo,
					})
				} else {
					round.PendingTools = append(round.PendingTools, toolInfo)
				}
			default: // verbose
				b.emitter.EmitEvent(OutboundEvent{
					Kind:    OutboundEventToolResult,
					ToolRes: &toolInfo,
				})
			}
			b.emitter.TriggerTyping()
			if sink != nil {
				sink.OnToolResult(event.Tool.Name, string(event.Tool.Arguments), event.Result, event.IsError)
			}

		case provider.StreamEventDone:
			text := round.Text()
			mode := b.resolveEffectiveOutputMode()

			// For quiet/summary modes, emit aggregated tool summary
			if (mode == "quiet" || mode == "summary") && len(round.PendingTools) > 0 {
				summary := formatToolSummary(b.language, round.PendingTools, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
				if summary != "" {
					b.emitter.EmitText(summary)
				}
			}

			// In summary mode, only send the final LLM text
			// In quiet/verbose mode, always send the text
			if strings.TrimSpace(text) != "" {
				b.emitter.EmitRoundSummary(text, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
			}
			// AskUser is already emitted by HandleAskUser when the tool executes;
			// do not emit again here to avoid duplicate messages.
			// Save assistant messages to session
			b.appendAssistantMessages()
			round.Reset()
			if sink != nil {
				sink.OnRoundDone()
			}

		case provider.StreamEventError:
			if !errors.Is(event.Error, context.Canceled) {
				b.emitter.EmitText(provider.UserFacingError(event.Error))
			}
			if sink != nil {
				sink.OnError(event.Error)
			}
		}
	})
	return err
}

// checkAutoRunSuggestion logs whether the input should be routed to harness.
// In daemon mode this is informational — the agent uses skill instructions
// to decide whether to invoke harness. Future: integrate with harness run API.
func (b *DaemonBridge) checkAutoRunSuggestion(text string) {
	if b.harnessMode == "" || b.harnessMode == "off" || text == "" {
		return
	}
	ctx := harness.RouteContext{
		Input:      text,
		WorkingDir: b.workingDir,
	}
	// Build a minimal config for ShouldAutoRun
	cfg := &config.Config{Harness: config.HarnessConfig{
		AutoRun:  b.harnessMode,
		AutoInit: b.harnessAutoInit,
	}}
	result, err := harness.ShouldAutoRun(cfg, text, ctx)
	if err != nil || result == nil {
		return
	}
	if result.Decision == harness.RouteHarness || result.Decision == harness.RouteSuggest {
		debug.Log("daemon", "auto-run: %s → %v (project=%v)", truncate(text, 40), result.Decision, result.Project != nil)
	}
}

// truncate shortens s to maxLen for logging.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// tryHarnessAutoRun checks whether the input should be routed to harness
// and executes via RunService if so. Returns non-nil result when harness
// handled the request. Returns nil to fall through to normal agent run.
func (b *DaemonBridge) tryHarnessAutoRun(ctx context.Context, text string) *harness.RunServiceResult {
	routeCtx := harness.RouteContext{
		Input:                 text,
		WorkingDir:            b.workingDir,
		LLMClassifierProvider: b.agent.Provider(),
	}
	cfg := &config.Config{Harness: config.HarnessConfig{
		AutoRun:  b.harnessMode,
		AutoInit: b.harnessAutoInit,
	}}
	result, err := harness.ShouldAutoRun(cfg, text, routeCtx)
	if err != nil || result == nil {
		return nil
	}
	if result.Decision != harness.RouteHarness {
		// Suggest mode: let agent handle it (skill instructions guide the model)
		if result.Decision == harness.RouteSuggest {
			debug.Log("daemon", "auto-run suggest: %s", truncate(text, 60))
		}
		return nil
	}

	// Route to harness — emit status and run
	debug.Log("daemon", "auto-run harness: %s", truncate(text, 60))
	b.emitter.EmitText(fmt.Sprintf("harness auto-run: %s", truncate(text, 60)))

	svc := harness.NewRunService()
	if result.Project == nil {
		debug.Log("daemon", "auto-run: RouteHarness but no project — falling through to agent")
		return nil
	}
	project := *result.Project
	runResult := svc.Run(ctx, harness.RunServiceInput{
		Project: project,
		Config:  result.Config,
		Goal:    text,
		Runner:  harness.BinaryRunner{},
		Options: harness.RunTaskOptions{},
	})

	// Emit the result
	output := harness.FormatRunServiceResult(runResult)
	b.emitter.EmitText(output)
	return runResult
}

// appendUserMessage adds the user message to the session store.
func (b *DaemonBridge) appendUserMessage(content []provider.ContentBlock) {
	if b.store == nil || b.sess == nil {
		return
	}
	text := contentToText(content)
	msg := provider.Message{
		Role:    "user",
		Content: content,
	}
	if b.sess.Title == "" || b.sess.Title == "New session" {
		if len(text) > 60 {
			b.sess.Title = truncateRunes(text, 60, "...")
		} else {
			b.sess.Title = text
		}
	}
	if s, ok := b.store.(*session.JSONLStore); ok {
		_ = s.AppendMessage(b.sess, msg)
	} else {
		b.sess.Messages = append(b.sess.Messages, msg)
	}
}

// appendAssistantMessages saves new agent messages to the session JSONL.
// ⚠️ Only appends new messages via AppendMessage — NEVER overwrites
// b.sess.Messages with agent.Messages() (compacted). Overwriting would
// lose pre-compaction history. Instead, append only what's new beyond
// what ses.Messages already holds.
func (b *DaemonBridge) appendAssistantMessages() {
	if b.store == nil || b.sess == nil || b.agent == nil {
		return
	}
	messages := b.agent.Messages()
	sessLen := len(b.sess.Messages)
	// After compaction, agent may have FEWER messages than ses.Messages.
	// In that case, the compacted messages are already covered by the
	// checkpoint record on disk. Only append truly new messages.
	if len(messages) <= sessLen {
		// Agent compacted or no new messages — nothing to append.
		return
	}
	// Append only new messages beyond what ses.Messages already has.
	for i := sessLen; i < len(messages); i++ {
		if s, ok := b.store.(*session.JSONLStore); ok {
			_ = s.AppendMessage(b.sess, messages[i])
		}
		b.sess.Messages = append(b.sess.Messages, messages[i])
	}
}

func (b *DaemonBridge) recordMetric(ev metrics.MetricEvent) {
	b.mu.Lock()
	ses := b.sess
	store := b.store
	ev.TurnIndex = b.usageTurnIndex
	if ses != nil {
		ev.Model = ses.Model
		ev.Vendor = ses.Vendor
		ev.Endpoint = ses.Endpoint
		ses.Metrics = append(ses.Metrics, ev)
		ses.AppendMetricForEndpoint(ses.Vendor, ses.Endpoint, ev)
	}
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetric(ses, ev)
	}
}

func (b *DaemonBridge) Close() {
	b.mu.Lock()
	collector := b.metricCollector
	cancel := b.metricCancel
	b.metricCollector = nil
	b.metricCancel = nil
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if collector != nil {
		collector.Stop()
	}
}

func daemonSessionTurnIndex(ses *session.Session) int {
	if ses == nil {
		return 0
	}
	last := 0
	if n := len(ses.UsageHistory); n > 0 && ses.UsageHistory[n-1].TurnIndex > last {
		last = ses.UsageHistory[n-1].TurnIndex
	}
	if n := len(ses.Metrics); n > 0 && ses.Metrics[n-1].TurnIndex > last {
		last = ses.Metrics[n-1].TurnIndex
	}
	return last
}

// --- helpers ---

type daemonRoundState = SummaryRoundState

// isDaemonSkippedTool returns true for sub-agent lifecycle tools that
// should not be counted as visible tool calls in the daemon IM status.
func isDaemonSkippedTool(name string) bool {
	switch name {
	case "spawn_agent", "wait_agent", "list_agents":
		return true
	}
	return false
}

// contentToText extracts plain text from content blocks.
func contentToText(blocks []provider.ContentBlock) string {
	var parts []string
	for _, b := range blocks {
		if t := strings.TrimSpace(b.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

func jsonMarshalArgs(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// formatToolSummary renders an aggregated tool execution summary for quiet/summary IM modes.
func formatToolSummary(lang string, tools []ToolResultInfo, total, successes, failures int) string {
	if len(tools) == 0 {
		return ""
	}

	var sb strings.Builder

	// Header line
	if ToolLanguage(lang) == ToolLangZhCN {
		sb.WriteString(fmt.Sprintf("⚙️ 执行了 %d 个工具调用", total))
		if failures > 0 {
			sb.WriteString(fmt.Sprintf("（%d 成功，%d 失败）", successes, failures))
		}
	} else {
		sb.WriteString(fmt.Sprintf("⚙️ %d tool calls", total))
		if failures > 0 {
			sb.WriteString(fmt.Sprintf(" (%d ok, %d failed)", successes, failures))
		}
	}
	sb.WriteString("\n")

	// Aggregate tool names with counts
	toolCounts := make(map[string]int)
	for _, t := range tools {
		name := formatIMToolDisplayName(t.ToolName)
		toolCounts[name]++
	}

	// Sort by count descending
	type kv struct {
		name  string
		count int
	}
	var sorted []kv
	for k, v := range toolCounts {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	for _, item := range sorted {
		if item.count == 1 {
			sb.WriteString(fmt.Sprintf("  ✓ %s\n", item.name))
		} else {
			sb.WriteString(fmt.Sprintf("  ✓ %s ×%d\n", item.name, item.count))
		}
	}

	return sb.String()
}

// formatIMToolDisplayName returns a short display name for a tool.
func formatIMToolDisplayName(toolName string) string {
	lang := ToolLanguage("")
	pres := DescribeTool(lang, toolName, "")
	if pres.DisplayName != "" {
		return pres.DisplayName
	}
	return toolName
}

// handleSlashCommand processes IM slash commands.
func (b *DaemonBridge) handleSlashCommand(ctx context.Context, text string, msg InboundMessage) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "/restart") {
		b.mu.Lock()
		onRestart := b.onRestart
		b.mu.Unlock()
		if onRestart == nil {
			return fmt.Errorf("restart not available")
		}
	}

	if result := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
		Manager:     b.manager,
		SelfAdapter: msg.Envelope.Adapter,
		Text:        text,
		HelpExtraLines: []string{
			"/restart [debug] - Restart daemon (unmutes all adapters; add 'debug' to enable GGCODE_DEBUG=1)",
			"/provider [vendor] [endpoint] - Show or switch LLM provider",
			"/model [name] - Show or switch model",
			"/config - Show current provider and model configuration",
		},
		OnRestart: func(debugMode bool) (string, error) {
			b.mu.Lock()
			onRestart := b.onRestart
			b.mu.Unlock()
			if onRestart == nil {
				return "", fmt.Errorf("restart not available")
			}
			if debugMode {
				b.mu.Lock()
				b.restartDebug = true
				b.mu.Unlock()
				safego.Go("im.daemonBridge.restart", func() {
					time.Sleep(1 * time.Second)
					onRestart()
				})
				return "🔄 Restarting daemon with debug logging (GGCODE_DEBUG=1)...", nil
			}
			safego.Go("im.daemonBridge.restart", func() {
				time.Sleep(1 * time.Second)
				onRestart()
			})
			return "🔄 Restarting daemon...", nil
		},
		OnProvider: func(vendor, endpoint string) (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("provider switching not available in this mode")
			}
			return fn(vendor, endpoint, "")
		},
		OnModel: func(model string) (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("model switching not available in this mode")
			}
			return fn("", "", model)
		},
		OnConfig: func() (string, error) {
			b.mu.Lock()
			fn := b.onProviderSwitch
			b.mu.Unlock()
			if fn == nil {
				return "", fmt.Errorf("config display not available in this mode")
			}
			return fn("", "", "")
		},
	}); result.Handled {
		if result.Response != "" {
			b.emitter.EmitText(result.Response)
		}
		if result.MuteSelfAdapter != "" {
			time.Sleep(500 * time.Millisecond)
			b.manager.MuteBinding(result.MuteSelfAdapter)
		}
		return nil
	}
	return nil
}

func (b *DaemonBridge) handleProviderCommand(parts []string) error {
	b.mu.Lock()
	fn := b.onProviderSwitch
	b.mu.Unlock()
	if fn == nil {
		b.emitter.EmitText("❌ Provider switching not available in this mode.")
		return nil
	}

	vendor := ""
	endpoint := ""
	if len(parts) > 1 {
		vendor = parts[1]
	}
	if len(parts) > 2 {
		endpoint = parts[2]
	}

	summary, err := fn(vendor, endpoint, "")
	if err != nil {
		b.emitter.EmitText(fmt.Sprintf("❌ %s", err))
		return nil
	}
	b.emitter.EmitText(summary)
	return nil
}

func (b *DaemonBridge) handleModelCommand(parts []string) error {
	b.mu.Lock()
	fn := b.onProviderSwitch
	b.mu.Unlock()
	if fn == nil {
		b.emitter.EmitText("❌ Model switching not available in this mode.")
		return nil
	}

	model := ""
	if len(parts) > 1 {
		model = parts[1]
	}

	summary, err := fn("", "", model)
	if err != nil {
		b.emitter.EmitText(fmt.Sprintf("❌ %s", err))
		return nil
	}
	b.emitter.EmitText(summary)
	return nil
}

// resolveEffectiveOutputMode returns the output mode considering the
// platform-specific defaults. If any bound adapter's platform requires
// a different mode (e.g. WeChat -> summary), that overrides the global mode.
func (b *DaemonBridge) resolveEffectiveOutputMode() string {
	globalMode := b.emitter.OutputMode()
	if b.manager == nil {
		return globalMode
	}
	snapshot := b.manager.Snapshot()
	for _, binding := range snapshot.CurrentBindings {
		if binding.Muted {
			continue
		}
		for _, adapter := range snapshot.Adapters {
			if adapter.Name == binding.Adapter && adapter.Platform == PlatformWechat {
				if globalMode == "" || globalMode == "verbose" {
					return WechatDefaultOutputMode
				}
			}
		}
	}
	return globalMode
}

func (b *DaemonBridge) handleConfigCommand() error {
	b.mu.Lock()
	fn := b.onProviderSwitch
	b.mu.Unlock()
	if fn == nil {
		b.emitter.EmitText("❌ Config display not available in this mode.")
		return nil
	}

	// Call with all empty → returns current config summary
	summary, err := fn("", "", "")
	if err != nil {
		b.emitter.EmitText(fmt.Sprintf("❌ %s", err))
		return nil
	}
	b.emitter.EmitText(summary)
	return nil
}

// --- ChatBridge implementation (for webui WebChat) ---

// extractText returns the concatenated text from content blocks.
func extractText(blocks []provider.ContentBlock) string {
	var text string
	for _, b := range blocks {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return text
}

// Messages returns the current agent conversation history.
func (b *DaemonBridge) Messages() []provider.Message {
	return b.agent.Messages()
}

// SendUserMessage injects a user message into the agent conversation.
// If the agent is currently running, the message is queued as an interruption
// (same mechanism as IM mid-run messages). If the agent is idle, a new run
// is started.
func (b *DaemonBridge) SendUserMessage(content []provider.ContentBlock) {
	text := extractText(content)
	if text == "" && len(content) == 0 {
		return
	}
	b.notifyUserMessage(content)

	// Notify activity hook (Knight idle timer) for webchat messages too.
	if text != "" {
		b.mu.Lock()
		onActivity := b.onActivity
		b.mu.Unlock()
		if onActivity != nil {
			onActivity()
		}
	}

	// tryQueueOrBeginRun atomically checks if the agent is busy (queue
	// the interruption) or idle (begin a new run slot). This eliminates
	// the TOCTOU window between separate tryQueueInterruption and
	// beginRunSlot calls.
	ctx2, queued := b.tryQueueOrBeginRun(content, "webchat: ")
	if queued {
		return
	}
	b.notifyRunStateChange(true)

	// Start the run outside the lock
	safego.Go("im.daemonBridge.run", func() {
		b.runQueuedLoop(ctx2, content, "webchat: ", func(ctx context.Context, text string) bool {
			if text != "" && b.harnessMode != "" && b.harnessMode != "off" {
				return b.tryHarnessAutoRun(ctx, text) != nil
			}
			return false
		}, func(err error) {
			debug.Log("daemon-bridge", "webchat: agent run error: %v", err)
		})
	})
}

type daemonBridgeSub struct {
	fn   func(provider.StreamEvent)
	ch   chan provider.StreamEvent
	done chan struct{}
}

// Subscribe registers a callback for agent streaming events.
// All events from the agent loop are forwarded to all subscribers
// via buffered channels to avoid blocking the agent.
// Returns an unsubscribe function.
func (b *DaemonBridge) Subscribe(fn func(provider.StreamEvent)) func() {
	sub := &daemonBridgeSub{
		fn:   fn,
		ch:   make(chan provider.StreamEvent, 256),
		done: make(chan struct{}),
	}
	// Start async forwarder goroutine
	safego.Go("im.daemonBridge.subscriber", func() {
		defer close(sub.done)
		for event := range sub.ch {
			func() {
				defer safego.Recover("im.daemonBridge.subscriberCallback")
				sub.fn(event)
			}()
		}
	})

	b.eventSubMu.Lock()
	defer b.eventSubMu.Unlock()
	b.eventSubs = append(b.eventSubs, sub)
	idx := len(b.eventSubs) - 1
	return func() {
		b.eventSubMu.Lock()
		defer b.eventSubMu.Unlock()
		if b.eventSubs[idx] != nil {
			close(b.eventSubs[idx].ch)
			<-b.eventSubs[idx].done // wait for drain
			b.eventSubs[idx] = nil
		}
	}
}

// broadcastEvent sends an event to all registered webchat subscribers.
func (b *DaemonBridge) broadcastEvent(event provider.StreamEvent) {
	b.eventSubMu.RLock()
	defer b.eventSubMu.RUnlock()
	for _, sub := range b.eventSubs {
		if sub != nil {
			select {
			case sub.ch <- event:
			default:
				debug.Log("daemon-bridge", "webchat subscriber channel full, dropping event")
			}
		}
	}
}
