package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

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

	mu         sync.Mutex
	cancelFunc context.CancelFunc
	pendingAsk *pendingAskUser
	followSink daemon.FollowSink
	onActivity func()
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

	// Cancel previous run if active
	b.mu.Lock()
	if b.cancelFunc != nil {
		b.cancelFunc()
	}
	ctx2, cancel := context.WithCancel(ctx)
	b.cancelFunc = cancel
	b.mu.Unlock()

	// Run agent in this goroutine (blocks until done)
	err := b.runAgentStream(ctx2, content)

	b.mu.Lock()
	b.cancelFunc = nil
	b.mu.Unlock()

	if err != nil && !errors.Is(err, context.Canceled) {
		b.emitter.EmitText(provider.UserFacingError(err))
	}
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
			// so the user sees it before the tool blocks. The result
			// is suppressed in formatSpecialIMToolResult.
			if toolName == "sleep" {
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
			b.emitter.EmitEvent(OutboundEvent{
				Kind: OutboundEventToolResult,
				ToolRes: &ToolResultInfo{
					ToolName: event.Tool.Name,
					Args:     string(event.Tool.Arguments),
					Result:   event.Result,
					IsError:  event.IsError,
				},
			})
			b.emitter.TriggerTyping()
			if b.followSink != nil {
				b.followSink.OnToolResult(event.Tool.Name, string(event.Tool.Arguments), event.Result, event.IsError)
			}

		case provider.StreamEventDone:
			text := round.Text()
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
