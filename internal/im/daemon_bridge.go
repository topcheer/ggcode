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
	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// pendingAskUser tracks an in-flight ask_user request waiting for an IM reply.
type pendingAskUser struct {
	request  toolpkg.AskUserRequest
	response chan toolpkg.AskUserResponse
}

// DaemonBridge implements the Bridge interface for headless (daemon) mode.
// IM inbound messages are submitted directly to the agent without a TUI.
type DaemonBridge struct {
	manager  *Manager
	emitter  *IMEmitter
	agent    *agent.Agent
	store    session.Store
	sess     *session.Session
	language string

	mu                   sync.Mutex
	cancelFunc           context.CancelFunc
	pendingAsk           *pendingAskUser
	pendingInterruptions []string
	followSink           daemon.FollowSink
	onActivity           func()
	onRestart            func() // trigger daemon self-restart
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
	return b
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

	// Slash commands
	if strings.HasPrefix(text, "/") {
		return b.handleSlashCommand(ctx, text, msg)
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

	// If an agent run is already active, inject as interruption instead of
	// cancelling the running stream. The agent's main loop will pick up the
	// message after the current tool call / LLM round finishes.
	b.mu.Lock()
	activeCancel := b.cancelFunc
	b.mu.Unlock()

	if activeCancel != nil {
		debug.Log("daemon-bridge", "agent running, queuing interruption: %s", truncateStr(text, 80))
		b.mu.Lock()
		b.pendingInterruptions = append(b.pendingInterruptions, text)
		b.mu.Unlock()
		return nil
	}

	// No active run — start a new one.
	ctx2, cancel := context.WithCancel(ctx)

	// Set up interruption handler so mid-run IM messages get injected
	// into the agent's conversation instead of cancelling the stream.
	b.mu.Lock()
	b.pendingInterruptions = b.pendingInterruptions[:0] // clear stale
	b.agent.SetInterruptionHandler(func() string {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.pendingInterruptions) == 0 {
			return ""
		}
		msg := b.pendingInterruptions[0]
		b.pendingInterruptions = b.pendingInterruptions[1:]
		return msg
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
		var nextText string
		if len(b.pendingInterruptions) > 0 {
			nextText = b.pendingInterruptions[0]
			b.pendingInterruptions = b.pendingInterruptions[1:]
		}
		b.mu.Unlock()

		if nextText == "" {
			break
		}

		debug.Log("daemon-bridge", "draining queued message for next round: %s", truncateStr(nextText, 80))
		content = []provider.ContentBlock{{Type: "text", Text: nextText}}
	}

	b.mu.Lock()
	b.cancelFunc = nil
	b.agent.SetInterruptionHandler(nil)
	b.mu.Unlock()

	return nil
}

// HandleAskUser is the AskUserHandler for daemon mode — sends questions to IM
// one at a time and collects answers interactively.
func (b *DaemonBridge) HandleAskUser(ctx context.Context, req toolpkg.AskUserRequest) (toolpkg.AskUserResponse, error) {
	answers := make([]toolpkg.AskUserAnswer, len(req.Questions))
	answeredCount := 0

	for i, q := range req.Questions {
		// Format and send a single question
		singleReq := toolpkg.AskUserRequest{
			Title:     req.Title,
			Questions: []toolpkg.AskUserQuestion{q},
		}
		argsJSON, _ := jsonMarshalArgs(singleReq)
		text := b.emitter.FormatAskUserPrompt(argsJSON)
		if text == "" {
			text = strings.TrimSpace(q.Title)
		}
		if text != "" {
			b.emitter.EmitAskUser(text)
		}

		// Block until the user replies via IM
		pending := &pendingAskUser{
			request:  req,
			response: make(chan toolpkg.AskUserResponse, 1),
		}
		b.mu.Lock()
		b.pendingAsk = pending
		b.mu.Unlock()

		select {
		case resp := <-pending.response:
			// Extract the answer for this question
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

// runAgentStream executes the agent with streaming output sent to IM.
// IM messages mirror TUI behavior exactly: text is only emitted on
// StreamEventDone (not per-token), tool status is emitted as it happens.
func (b *DaemonBridge) runAgentStream(ctx context.Context, content []provider.ContentBlock) error {
	round := &daemonRoundState{}

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
		return msg
	})
	defer b.agent.SetInterruptionHandler(nil)

	// Save user message to session
	b.appendUserMessage(content)

	err := b.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
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
			if toolName == "sleep" && b.emitter.OutputMode() == "verbose" {
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

			switch b.emitter.OutputMode() {
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
			mode := b.emitter.OutputMode()

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
			b.emitter.EmitText("🔄 Restarting daemon...")
			go func() {
				time.Sleep(1 * time.Second)
				onRestart()
			}()
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
			"/restart - Restart daemon (unmutes all adapters)\n" +
			"/help - Show this help")
		return nil

	default:
		b.emitter.EmitText("Unknown command: " + cmd + ". Try /help")
		return nil
	}
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
