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

	mu                   sync.Mutex
	cancelFunc           context.CancelFunc
	pendingAsk           *pendingAskUser
	pendingApproval      chan permission.Decision // non-nil when waiting for IM approval reply
	pendingInterruptions []pendingInterruption
	interactiveMsgIDs    map[string]string // adapter → platform msg ID (for callback correlation)
	multiSelectChosen    map[string]bool   // accumulated multi-select choices (choice value → selected)
	followSink           daemon.FollowSink
	onActivity           func()
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
			b.metricCollector = metrics.NewCollector(256, func(ev metrics.MetricEvent) {
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
	resp := buildAskUserResponse(pending.request, text)
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

	// Slash commands take priority over everything (including pending approval/ask_user)
	if strings.HasPrefix(text, "/") {
		return b.handleSlashCommand(ctx, text, msg)
	}

	// Check for pending approval — y/a/n reply for tool permission
	b.mu.Lock()
	approvalCh := b.pendingApproval
	b.mu.Unlock()
	if approvalCh != nil && text != "" {
		decision, ok := parseDaemonApprovalReply(text)
		if ok {
			approvalCh <- decision
			b.mu.Lock()
			b.pendingApproval = nil
			b.mu.Unlock()
			return nil
		}
		// Not a valid approval reply — fall through to ask_user or normal message
	}

	// Check for pending ask_user — if so, route reply there
	b.mu.Lock()
	pending := b.pendingAsk
	b.mu.Unlock()
	if pending != nil {
		if text != "" {
			resp := buildAskUserResponse(pending.request, text)
			pending.response <- resp
			b.mu.Lock()
			b.pendingAsk = nil
			b.mu.Unlock()
			return nil
		}
	}

	// Normal agent submission
	content := msg.ProviderContent()
	if len(content) == 0 {
		return nil
	}

	// Check text is not empty
	if text == "" {
		return nil
	}

	// Immediately trigger typing indicator so the user sees feedback
	// before waiting for the first LLM token.
	b.emitter.TriggerTyping()

	// Notify follow sink of user message
	if b.followSink != nil {
		b.followSink.OnUserMessage(text)
	}

	// Atomically check for an active run and either queue as interruption
	// or claim the run slot. This eliminates the TOCTOU window where two
	// concurrent IM messages could both see cancelFunc==nil and start
	// duplicate agent runs.
	b.mu.Lock()

	if b.cancelFunc != nil {
		// Agent run already active — queue as interruption.
		debug.Log("daemon-bridge", "agent running, queuing interruption: %s", truncateStr(text, 80))
		b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{
			Content: []provider.ContentBlock{{Type: "text", Text: text}},
		})
		b.mu.Unlock()
		return nil
	}

	// No active run — claim the slot under a single lock acquisition.
	// Use context.Background() rather than the caller's ctx because the
	// agent run (including ask_user waiting for IM replies) must survive
	// beyond the lifetime of a single WS event callback. The caller's ctx
	// is typically tied to a Feishu/Telegram SDK event and expires when
	// that event processing completes or the WS reconnects.
	ctx2, cancel := context.WithCancel(context.Background())

	b.pendingInterruptions = b.pendingInterruptions[:0] // clear stale
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
	b.mu.Unlock()

	// Run agent (may loop if interruptions arrived mid-run).
	for {
		err := b.runAgentStream(ctx2, content)

		if err != nil && !errors.Is(err, context.Canceled) {
			b.emitter.EmitText(provider.UserFacingError(err))
		}

		// Check if more messages arrived during the run.
		b.mu.Lock()
		var nextContent []provider.ContentBlock
		if len(b.pendingInterruptions) > 0 {
			nextContent = b.pendingInterruptions[0].Content
			b.pendingInterruptions = b.pendingInterruptions[1:]
		}
		b.mu.Unlock()

		if len(nextContent) == 0 {
			break
		}

		nextText := extractText(nextContent)
		debug.Log("daemon-bridge", "draining queued message for next round: %s", truncateStr(nextText, 80))
		content = nextContent
	}

	b.mu.Lock()
	b.cancelFunc = nil
	b.agent.SetInterruptionHandler(nil)
	b.mu.Unlock()

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
	detail := formatToolInline(toolName, input)
	lang := b.language
	var prompt string
	if lang == "zh-CN" || lang == "zh" {
		prompt = fmt.Sprintf("🔒 需要审批: %s\n\n回复 y 允许 · a 总是允许 · n 拒绝", detail)
	} else {
		prompt = fmt.Sprintf("🔒 Approval required: %s\n\nReply y allow · a always allow · n deny", detail)
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
			switch decisionStr {
			case "allow":
				resultMsg = fmt.Sprintf("✅ 已允许: %s", formatToolInline(toolName, ""))
			case "deny":
				resultMsg = fmt.Sprintf("❌ 已拒绝: %s", formatToolInline(toolName, ""))
			}
		} else {
			switch decisionStr {
			case "allow":
				resultMsg = fmt.Sprintf("✅ Allowed: %s", formatToolInline(toolName, ""))
			case "deny":
				resultMsg = fmt.Sprintf("❌ Denied: %s", formatToolInline(toolName, ""))
			}
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
	b.mu.Unlock()

	// Set up interruption handler so mid-run IM messages get injected
	// into the agent's context instead of cancelling the stream.
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
	defer b.agent.SetInterruptionHandler(nil)

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
			if b.followSink != nil {
				b.followSink.OnStreamText(event.Text)
			}

		case provider.StreamEventToolCallDone:
			toolName := strings.TrimSpace(event.Tool.Name)
			if toolName == "ask_user" {
				round.SetAskUser(b.emitter.FormatAskUserPrompt(string(event.Tool.Arguments)))
			}
			if !isDaemonSkippedTool(toolName) {
				round.ToolCalls++
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
			if event.IsError {
				round.ToolFailures++
			} else {
				round.ToolSuccesses++
			}
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
				round.pendingTools = append(round.pendingTools, toolInfo)
			case "quiet":
				// Buffer for aggregation; errors still sent immediately
				if event.IsError {
					b.emitter.EmitEvent(OutboundEvent{
						Kind:    OutboundEventToolResult,
						ToolRes: &toolInfo,
					})
				} else {
					round.pendingTools = append(round.pendingTools, toolInfo)
				}
			default: // verbose
				b.emitter.EmitEvent(OutboundEvent{
					Kind:    OutboundEventToolResult,
					ToolRes: &toolInfo,
				})
			}
			b.emitter.TriggerTyping()
			if b.followSink != nil {
				b.followSink.OnToolResult(event.Tool.Name, string(event.Tool.Arguments), event.Result, event.IsError)
			}

		case provider.StreamEventDone:
			text := round.Text()
			mode := b.resolveEffectiveOutputMode()

			// For quiet/summary modes, emit aggregated tool summary
			if (mode == "quiet" || mode == "summary") && len(round.pendingTools) > 0 {
				summary := formatToolSummary(b.language, round.pendingTools, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
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
			if b.followSink != nil {
				b.followSink.OnRoundDone()
			}

		case provider.StreamEventError:
			if !errors.Is(event.Error, context.Canceled) {
				b.emitter.EmitText(provider.UserFacingError(event.Error))
			}
			if b.followSink != nil {
				b.followSink.OnError(event.Error)
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
			b.sess.Title = text[:57] + "..."
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

// appendAssistantMessages saves the current agent messages to the session.
func (b *DaemonBridge) appendAssistantMessages() {
	if b.store == nil || b.sess == nil || b.agent == nil {
		return
	}
	messages := b.agent.Messages()
	// Append only new messages since last save
	start := len(b.sess.Messages)
	if start > len(messages) {
		start = 0
	}
	for i := start; i < len(messages); i++ {
		if s, ok := b.store.(*session.JSONLStore); ok {
			_ = s.AppendMessage(b.sess, messages[i])
		}
	}
	b.sess.Messages = messages
}

func (b *DaemonBridge) recordMetric(ev metrics.MetricEvent) {
	b.mu.Lock()
	ses := b.sess
	store := b.store
	ev.TurnIndex = b.usageTurnIndex
	if ses != nil {
		ses.Metrics = append(ses.Metrics, ev)
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
	b.metricCollector = nil
	b.mu.Unlock()
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

type daemonRoundState struct {
	text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
	AskUserText   string
	// Buffered tool results for quiet/summary mode
	pendingTools []ToolResultInfo
}

func (s *daemonRoundState) AppendText(t string)    { s.text.WriteString(t) }
func (s *daemonRoundState) Text() string           { return s.text.String() }
func (s *daemonRoundState) SetAskUser(t string)    { s.AskUserText = strings.TrimSpace(t) }
func (s *daemonRoundState) HasVisibleOutput() bool { return strings.TrimSpace(s.Text()) != "" }
func (s *daemonRoundState) Reset() {
	s.text.Reset()
	s.ToolCalls = 0
	s.ToolSuccesses = 0
	s.ToolFailures = 0
	s.AskUserText = ""
	s.pendingTools = nil
}

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

// buildAskUserResponse constructs an AskUserResponse from user IM text.
// Uses the shared ParseMultiQuestionReply for multi-question support and
// ParseRemoteQuestionnaireAnswer for single-question parsing.
func buildAskUserResponse(req toolpkg.AskUserRequest, text string) toolpkg.AskUserResponse {
	if len(req.Questions) == 1 {
		// Single question: parse directly
		q := req.Questions[0]
		selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, q)
		parsed := []ParsedQuestionAnswer{{
			QuestionIndex: 0,
			Selected:      selected,
			Freeform:      freeform,
			Error:         err,
		}}
		return BuildAskUserResponse(req, parsed)
	}

	// Multi-question: try multi-line split first
	lines := SplitNonEmptyLines(text)
	if len(lines) >= len(req.Questions) {
		parsed := ParseMultiQuestionReply(text, req.Questions)
		allOK := true
		for _, p := range parsed {
			if p.Error != nil {
				allOK = false
				break
			}
		}
		if allOK {
			return BuildAskUserResponse(req, parsed)
		}
	}

	// Fallback: treat entire text as freeform answer to all questions
	parsed := make([]ParsedQuestionAnswer, len(req.Questions))
	for i, q := range req.Questions {
		selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, q)
		parsed[i] = ParsedQuestionAnswer{
			QuestionIndex: i,
			Selected:      selected,
			Freeform:      freeform,
			Error:         err,
		}
	}
	return BuildAskUserResponse(req, parsed)
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
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/restart":
		b.mu.Lock()
		onRestart := b.onRestart
		b.mu.Unlock()
		if onRestart != nil {
			// /restart debug → enable debug logging on next launch
			if len(parts) > 1 && strings.ToLower(parts[1]) == "debug" {
				b.mu.Lock()
				b.restartDebug = true
				b.mu.Unlock()
				b.emitter.EmitText("🔄 Restarting daemon with debug logging (GGCODE_DEBUG=1)...")
			} else {
				b.emitter.EmitText("🔄 Restarting daemon...")
			}
			safego.Go("im.daemonBridge.restart", func() {
				time.Sleep(1 * time.Second)
				onRestart()
			})
			return nil
		}
		return fmt.Errorf("restart not available")

	case "/listim":
		return b.handleListIM()

	case "/muteim":
		if len(parts) < 2 {
			b.emitter.EmitText("Usage: /muteim <adapter_name>\nUse /listim to see adapter names.")
			return nil
		}
		return b.handleMuteIM(parts[1], msg)

	case "/muteall":
		return b.handleMuteAll(msg)

	case "/muteself":
		return b.handleMuteSelf(msg)

	case "/help":
		b.emitter.EmitText("Available commands:\n" +
			"/listim - List IM adapters and their status\n" +
			"/muteim <name> - Mute a specific adapter\n" +
			"/muteall - Mute all adapters except the one you're using\n" +
			"/muteself - Mute THIS adapter (⚠️ you'll stop receiving replies; use /restart from another adapter to recover)\n" +
			"/restart [debug] - Restart daemon (unmutes all adapters; add 'debug' to enable GGCODE_DEBUG=1)\n" +
			"/provider [vendor] [endpoint] - Show or switch LLM provider\n" +
			"/model [name] - Show or switch model\n" +
			"/config - Show current provider and model configuration\n" +
			"/help - Show this help")
		return nil

	case "/provider":
		return b.handleProviderCommand(parts)

	case "/model":
		return b.handleModelCommand(parts)

	case "/config":
		return b.handleConfigCommand()

	default:
		b.emitter.EmitText("Unknown command: " + cmd + ". Try /help")
		return nil
	}
}

// parseDaemonApprovalReply parses an IM text reply as an approval decision.
func parseDaemonApprovalReply(text string) (permission.Decision, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "y", "yes", "ok", "好", "好的", "允许", "同意", "确认":
		return permission.Allow, true
	case "a", "always", "总是允许", "总是", "始终允许":
		return permission.Allow, true
	case "n", "no", "nope", "拒绝", "取消", "不要", "deny":
		return permission.Deny, true
	}
	if strings.HasPrefix(t, "y") && len(t) <= 3 {
		return permission.Allow, true
	}
	if strings.HasPrefix(t, "n") && len(t) <= 3 {
		return permission.Deny, true
	}
	return permission.Deny, false
}

func (b *DaemonBridge) handleListIM() error {
	snapshot := b.manager.Snapshot()

	if len(snapshot.Adapters) == 0 {
		b.emitter.EmitText("📭 No IM adapters configured.")
		return nil
	}

	var sb strings.Builder
	sb.WriteString("📬 IM Adapters:\n")

	for _, a := range snapshot.Adapters {
		status := "✅ online"
		if !a.Healthy {
			status = "❌ " + a.Status
		}

		// Check if bound
		bound := ""
		for _, binding := range snapshot.CurrentBindings {
			if binding.Adapter == a.Name {
				if binding.Muted {
					bound = " 🔇 muted"
				} else {
					bound = " 📡 active"
				}
				break
			}
		}

		sb.WriteString(fmt.Sprintf("  • %s [%s]%s %s\n", a.Name, a.Platform, bound, status))
	}

	b.emitter.EmitText(sb.String())
	return nil
}

func (b *DaemonBridge) handleMuteIM(adapterName string, msg InboundMessage) error {
	selfAdapter := msg.Envelope.Adapter
	if adapterName == selfAdapter {
		b.emitter.EmitText("⚠️ Cannot mute yourself with /muteim. Use /muteself instead.")
		return nil
	}

	if err := b.manager.MuteBinding(adapterName); err != nil {
		b.emitter.EmitText(fmt.Sprintf("❌ Failed to mute %s: %v", adapterName, err))
		return nil
	}

	b.emitter.EmitText(fmt.Sprintf("🔇 Muted adapter: %s", adapterName))
	return nil
}

func (b *DaemonBridge) handleMuteAll(msg InboundMessage) error {
	selfAdapter := msg.Envelope.Adapter

	count, err := b.manager.MuteAllExcept(selfAdapter)
	if err != nil {
		b.emitter.EmitText(fmt.Sprintf("❌ Failed to mute adapters: %v", err))
		return nil
	}

	if selfAdapter != "" {
		b.emitter.EmitText(fmt.Sprintf("🔇 Muted %d adapter(s), keeping %s active", count, selfAdapter))
	} else {
		b.emitter.EmitText(fmt.Sprintf("🔇 Muted %d adapter(s)", count))
	}
	return nil
}

func (b *DaemonBridge) handleMuteSelf(msg InboundMessage) error {
	selfAdapter := msg.Envelope.Adapter
	if selfAdapter == "" {
		b.emitter.EmitText("❌ Cannot determine your adapter name.")
		return nil
	}

	// Emit warning FIRST (before muting, so the message actually gets delivered)
	b.emitter.EmitText(fmt.Sprintf(
		"🔇 Muting adapter %s. You will NOT receive any more replies.\n"+
			"💡 Use /restart from another adapter to recover.",
		selfAdapter,
	))

	// Small delay to ensure the message is sent before we disconnect
	time.Sleep(500 * time.Millisecond)

	b.manager.MuteBinding(selfAdapter)
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

	// Notify activity hook (Knight idle timer) for webchat messages too.
	if text != "" {
		b.mu.Lock()
		onActivity := b.onActivity
		b.mu.Unlock()
		if onActivity != nil {
			onActivity()
		}
	}

	// Atomically check if agent is running and either queue interruption
	// or claim the "run starter" role. This prevents TOCTOU races with
	// concurrent IM messages.
	b.mu.Lock()
	activeCancel := b.cancelFunc
	if activeCancel != nil {
		// Agent is running — inject as interruption
		debug.Log("daemon-bridge", "webchat: queuing interruption: %s", truncateStr(text, 80))
		b.pendingInterruptions = append(b.pendingInterruptions, pendingInterruption{Content: content})
		b.mu.Unlock()
		return
	}

	// Agent is idle — claim the run slot under the lock
	ctx2, cancel := context.WithCancel(context.Background())
	b.pendingInterruptions = b.pendingInterruptions[:0] // clear stale
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
	b.mu.Unlock()

	// Start the run outside the lock
	safego.Go("im.daemonBridge.run", func() {
		defer func() {
			b.mu.Lock()
			b.cancelFunc = nil
			b.agent.SetInterruptionHandler(nil)
			b.mu.Unlock()
		}()
		// Check if this input should be routed to harness auto-run
		if text != "" && b.harnessMode != "" && b.harnessMode != "off" {
			if harnessResult := b.tryHarnessAutoRun(ctx2, text); harnessResult != nil {
				return
			}
		}
		for {
			err := b.runAgentStream(ctx2, content)
			if err != nil && !errors.Is(err, context.Canceled) {
				debug.Log("daemon-bridge", "webchat: agent run error: %v", err)
			}

			b.mu.Lock()
			var nextContent []provider.ContentBlock
			if len(b.pendingInterruptions) > 0 {
				nextContent = b.pendingInterruptions[0].Content
				b.pendingInterruptions = b.pendingInterruptions[1:]
			}
			b.mu.Unlock()

			if len(nextContent) == 0 {
				break
			}
			debug.Log("daemon-bridge", "webchat: draining queued message: %s", truncateStr(extractText(nextContent), 80))
			content = nextContent
		}

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
