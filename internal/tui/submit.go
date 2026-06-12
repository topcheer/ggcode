package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/util"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

func (m *Model) appendUserMessage(text string) {
	m.sessionMutex().Lock()
	if m.session == nil || m.sessionStore == nil {
		m.sessionMutex().Unlock()
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	// Mutate Session object under sessionMutex to prevent data races
	// with checkpoint handler and other readers.
	m.session.Messages = append(m.session.Messages, msg)
	m.session.UpdatedAt = time.Now()
	// Auto-generate title from first user message
	if m.session.Title == "" || m.session.Title == "New session" {
		m.session.Title = util.Truncate(text, 60)
	}
	ses := m.session
	store := m.sessionStore
	m.sessionMutex().Unlock()

	// Echo to mobile tunnel client.
	m.pushTunnelUserMessage(text)

	// Persist to disk outside the lock.
	// JSONLStore.AppendMessageToDisk only writes the JSONL file + updates
	// the index (both protected by the store's own mu). It does NOT modify
	// the Session object, so no race with the TUI thread.
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMessageToDisk(ses, msg)
	} else {
		m.sessionMutex().Lock()
		_ = store.Save(m.session)
		m.sessionMutex().Unlock()
	}
}

func (m *Model) startAgent(text string) tea.Cmd {
	debug.Log("tui", "startAgent called: text=%s", util.Truncate(text, 200))
	m.usageTurnIndex++
	// Ensure the agent's provider is in sync with the current config.
	// This handles the case where the user set an API key in the provider
	// panel but hasn't explicitly activated — the key should still take effect.
	m.ensureProviderSync()
	m.rebuildSystemPrompt()

	// Capture and clear pending image
	img := m.pendingImage
	m.pendingImage = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeAgentRunID++
	runID := m.activeAgentRunID
	if m.agent != nil {
		m.agent.SetInterruptionHandler(func() string {
			return m.drainPendingInterrupt(runID)
		})
	}

	return func() tea.Msg {
		safego.Go("tui.startAgent.run", func() {
			defer func() {
				if m.metricCollectorFlush != nil {
					m.metricCollectorFlush()
				}
				if m.agent != nil {
					// Start background pre-compact only after the round-complete
					// signal is queued so its own metrics don't contaminate the
					// just-finished turn digest.
					defer m.agent.StartPreCompact()
				}
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

			m.pushInitialTunnelRunState()
			if err := m.runAgentSubmission(ctx, runID, text, img); err != nil && !errors.Is(err, context.Canceled) && m.program != nil {
				m.program.Send(agentErrMsg{RunID: runID, Err: err})
			}
		})

		return nil
	}
}

// startAgentWithExpand expands @mentions asynchronously then starts the agent.
// This avoids blocking the TUI update loop with filesystem I/O.
func (m *Model) startAgentWithExpand(text string) tea.Cmd {
	m.usageTurnIndex++
	m.rebuildSystemPrompt()
	img := m.pendingImage
	m.pendingImage = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeAgentRunID++
	runID := m.activeAgentRunID
	if m.agent != nil {
		m.agent.SetInterruptionHandler(func() string {
			return m.drainPendingInterrupt(runID)
		})
	}

	return func() tea.Msg {
		safego.Go("tui.startAgentWithExpand.run", func() {
			defer func() {
				if m.metricCollectorFlush != nil {
					m.metricCollectorFlush()
				}
				if m.agent != nil {
					defer m.agent.StartPreCompact()
				}
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

			m.pushInitialTunnelRunState()

			// Expand @mentions asynchronously
			workDir, _ := os.Getwd()
			expandedMsg, expandErr := ExpandMentions(text, workDir)
			if expandErr != nil && m.program != nil {
				m.program.Send(agentErrMsg{RunID: runID, Err: expandErr})
				return
			}

			if err := m.runAgentSubmission(ctx, runID, expandedMsg, img); err != nil && !errors.Is(err, context.Canceled) && m.program != nil {
				m.program.Send(agentErrMsg{RunID: runID, Err: err})
			}
		})

		return nil
	}
}

func (m *Model) pushInitialTunnelRunState() {
	initialTunnelStatus := m.currentTunnelStatus()
	m.pushTunnelStatus(initialTunnelStatus.Status, initialTunnelStatus.Message)
	m.pushTunnelCurrentActivity()
}

func (m *Model) runAgentSubmission(ctx context.Context, runID int, text string, img *imageAttachedMsg) error {
	content := buildAgentSubmissionContent(text, img, false)
	if img == nil {
		_, err := m.runAgentWithContent(ctx, runID, content)
		return err
	}

	if !m.activeEndpointSupportsVision() {
		_, err := m.runAgentWithContent(ctx, runID, content)
		return err
	}

	streamErrSent, err := m.runAgentWithContent(ctx, runID, buildAgentSubmissionContent(text, img, true))
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if streamErrSent || !provider.IsImageBlockFallbackCandidate(err) {
		return err
	}

	debug.Log("tui", "image block rejected, retrying without image block: %v", err)
	_, retryErr := m.runAgentWithContent(ctx, runID, content)
	if retryErr == nil {
		return nil
	}
	if errors.Is(retryErr, context.Canceled) {
		return retryErr
	}
	return err
}

// streamBatchInterval controls how often accumulated stream text is flushed
// to the TUI event loop.  80ms balances responsiveness vs. event-loop load.
const streamBatchInterval = 80 * time.Millisecond

func (m *Model) runAgentWithContent(ctx context.Context, runID int, content []provider.ContentBlock) (bool, error) {
	streamErrSent := false
	writingStatusSent := false
	round := agentIMRoundState{}

	// Stream batching: accumulate text chunks AND tool events, then flush
	// periodically instead of sending one program.Send per event.
	// This prevents event-loop saturation that stalls the spinner tick chain
	// and makes the TUI appear frozen — especially during burst tool calls.
	var batchMu sync.Mutex
	var batchBuf strings.Builder
	var batchReasoningBuf strings.Builder
	var fullReasoningBuf strings.Builder
	var toolBatchStatus []agentStatusMsg
	var toolBatchTools []agentToolStatusMsg
	batchDone := make(chan struct{})
	// closeBatchDone guarantees the batchDone channel is closed exactly once.
	// Without this, the StreamEventDone handler and the post-stream cleanup
	// can race (e.g. if a provider emits Done twice on retry, or if the
	// stream returns an error immediately after Done) and the second
	// close(chan) panics, killing the entire TUI process.
	var batchDoneOnce sync.Once
	closeBatchDone := func() {
		batchDoneOnce.Do(func() { close(batchDone) })
	}

	// flushBatch sends whatever has accumulated (text + tool events).
	// It is panic-protected because it calls into bubbletea's program.Send,
	// which can block / panic if the program is in a degraded state.
	flushBatch := func() {
		defer safego.Recover("tui.streamBatch.flush")
		batchMu.Lock()
		text := batchBuf.String()
		batchBuf.Reset()
		reasoningPending := batchReasoningBuf.Len() > 0
		batchReasoningBuf.Reset()
		reasoning := ""
		if reasoningPending {
			reasoning = fullReasoningBuf.String()
		}
		status := toolBatchStatus
		tools := toolBatchTools
		toolBatchStatus = nil
		toolBatchTools = nil
		batchMu.Unlock()

		if m.program == nil {
			return
		}
		for _, msg := range buildBatchedStreamMessages(runID, text, reasoning, status, tools) {
			m.program.Send(msg)
		}
	}

	// Background ticker that periodically flushes accumulated events.
	// Wrapped in safego so that any panic inside flushBatch (or in any of
	// the tea messages it constructs) is contained to this goroutine and
	// does not crash the TUI process.
	safego.Go("tui.streamBatch.ticker", func() {
		ticker := time.NewTicker(streamBatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				flushBatch()
			case <-batchDone:
				// Final flush before exit.
				flushBatch()
				return
			case <-ctx.Done():
				flushBatch()
				return
			}
		}
	})

	err := m.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
		defer safego.Recover("tui.streamCallback")
		// Broadcast to webchat subscribers
		if m.webuiBridge != nil {
			m.webuiBridge.BroadcastEvent(event)
		}
		// Push to mobile tunnel client
		m.pushTunnelEvent(event)
		if m.program == nil {
			return
		}
		switch event.Type {
		case provider.StreamEventText:
			// Always append to round state immediately (not throttled).
			round.AppendText(event.Text)
			// Accumulate for batched TUI delivery.
			batchMu.Lock()
			batchBuf.WriteString(event.Text)
			batchMu.Unlock()
			if !writingStatusSent {
				writingStatusSent = true
				m.program.Send(agentStatusMsg{RunID: runID, statusMsg: statusMsg{
					Activity: m.t("status.writing"),
				}})
			}
		case provider.StreamEventReasoning:
			if event.Text != "" && event.Text != "__redacted_thinking__" {
				batchMu.Lock()
				batchReasoningBuf.WriteString(event.Text)
				fullReasoningBuf.WriteString(event.Text)
				batchMu.Unlock()
			}

		case provider.StreamEventSystem:
			// System notification (retry status, etc.) — render as system message.
			flushBatch()
			m.program.Send(systemNotifyMsg{Text: event.Text})
		case provider.StreamEventToolCallDone:
			// Flush any pending text before tool events to keep output ordering correct.
			flushBatch()
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if event.Tool.Name == "ask_user" {
				round.SetAskUser(m.formatIMAskUserPrompt(string(event.Tool.Arguments)))
			}
			if isSubAgentLifecycleTool(event.Tool.Name) {
				// Sub-agent lifecycle tools are low-frequency; send directly.
				present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:      event.Tool.ID,
					ToolName:    event.Tool.Name,
					Activity:    m.t("status.thinking"),
					Running:     true,
					RawArgs:     string(event.Tool.Arguments),
					Args:        present.Detail,
					DisplayName: present.DisplayName,
				}})
				return
			}
			round.NoteToolCall()
			// Accumulate tool events for batched delivery.
			batchMu.Lock()
			toolBatchStatus = append(toolBatchStatus, agentStatusMsg{RunID: runID, statusMsg: statusMsg{
				Activity: present.Activity,
				ToolName: present.DisplayName,
				ToolArg:  present.Detail,
			}})
			toolBatchTools = append(toolBatchTools, agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
				ToolID:      event.Tool.ID,
				ToolName:    event.Tool.Name,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				Activity:    present.Activity,
				Running:     true,
				RawArgs:     string(event.Tool.Arguments),
				Args:        util.Truncate(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
			}})
			batchMu.Unlock()
		case provider.StreamEventToolResult:
			// Flush any pending text before tool results.
			flushBatch()
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if isSubAgentLifecycleTool(event.Tool.Name) {
				// Sub-agent lifecycle tools are low-frequency; send directly.
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:      event.Tool.ID,
					ToolName:    event.Tool.Name,
					Activity:    m.t("status.thinking"),
					Running:     false,
					Result:      event.Result,
					RawArgs:     string(event.Tool.Arguments),
					Args:        present.Detail,
					IsError:     event.IsError,
					DisplayName: present.DisplayName,
				}})
				m.program.Send(subAgentUpdateMsg{AgentID: extractAgentID(string(event.Tool.Arguments))})
				return
			}
			round.NoteToolResult(event.IsError)
			// Emit tool result to IM based on output mode.
			toolInfo := im.ToolResultInfo{
				ToolName: event.Tool.Name,
				Args:     string(event.Tool.Arguments),
				Result:   event.Result,
				IsError:  event.IsError,
				Detail:   present.Detail,
			}
			outputMode := "verbose"
			if m.imEmitter != nil {
				outputMode = m.imEmitter.OutputMode()
			}
			switch outputMode {
			case "summary":
				round.PendingTools = append(round.PendingTools, toolInfo)
			case "quiet":
				if event.IsError {
					m.emitIMEvent(im.OutboundEvent{Kind: im.OutboundEventToolResult, ToolRes: &toolInfo})
				} else {
					round.PendingTools = append(round.PendingTools, toolInfo)
				}
			default:
				m.emitIMEvent(im.OutboundEvent{Kind: im.OutboundEventToolResult, ToolRes: &toolInfo})
			}
			// Accumulate tool result events for batched delivery.
			batchMu.Lock()
			toolBatchStatus = append(toolBatchStatus, agentStatusMsg{RunID: runID, statusMsg: statusMsg{
				Activity: m.t("status.thinking"),
				ToolName: present.DisplayName,
				ToolArg:  present.Detail,
			}})
			toolBatchTools = append(toolBatchTools, agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
				ToolID:      event.Tool.ID,
				ToolName:    event.Tool.Name,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				Activity:    present.Activity,
				Running:     false,
				Result:      event.Result,
				RawArgs:     string(event.Tool.Arguments),
				Args:        util.Truncate(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
				IsError:     event.IsError,
			}})
			batchMu.Unlock()
		case provider.StreamEventError:
			if !errors.Is(event.Error, context.Canceled) {
				streamErrSent = true
				m.program.Send(agentErrMsg{RunID: runID, Err: sanitizeAPIError(event.Error)})
			}
		case provider.StreamEventDone:
			// Flush any remaining text/tool events synchronously before
			// processing the Done event. This is critical for multi-round
			// agent loops: each round ends with a Done event, and subsequent
			// rounds may contain pure text with no tool calls. Without this
			// flush, the text would remain stuck in batchBuf because the
			// ticker goroutine exits after the first closeBatchDone().
			flushBatch()
			m.program.Send(agentReasoningDoneMsg{})
			writingStatusSent = false
			switch {
			case round.AskUserText != "":
				m.program.Send(agentAskUserMsg{RunID: runID, Text: round.AskUserText})
			case round.HasVisibleOutput():
				m.program.Send(agentRoundSummaryMsg{
					RunID:         runID,
					Text:          round.Text(),
					ToolCalls:     round.ToolCalls,
					ToolSuccesses: round.ToolSuccesses,
					ToolFailures:  round.ToolFailures,
				})
			}
			round.Reset()
		}
	})

	// Ensure batch goroutine is stopped if RunStreamWithContent returns
	// without sending StreamEventDone (e.g. on error). Idempotent thanks to
	// closeBatchDone's sync.Once guard.
	closeBatchDone()

	if err != nil && !errors.Is(err, context.Canceled) && !streamErrSent {
		return false, err
	}
	return streamErrSent, err
}

func buildBatchedStreamMessages(runID int, text, reasoning string, status []agentStatusMsg, tools []agentToolStatusMsg) []tea.Msg {
	msgs := make([]tea.Msg, 0, 3)
	if text != "" {
		msgs = append(msgs, agentStreamMsg{RunID: runID, Text: text})
	}
	if reasoning != "" {
		msgs = append(msgs, agentReasoningMsg{RunID: runID, Text: reasoning})
	}
	if len(status) > 0 || len(tools) > 0 {
		msgs = append(msgs, agentToolBatchMsg{
			RunID:      runID,
			StatusMsgs: status,
			ToolMsgs:   tools,
		})
	}
	return msgs
}

type agentIMRoundState = im.SummaryRoundState

func buildAgentSubmissionContent(text string, img *imageAttachedMsg, includeImage bool) []provider.ContentBlock {
	prompt := strings.TrimSpace(text)
	if img == nil {
		return []provider.ContentBlock{provider.TextBlock(prompt)}
	}

	imagePath := strings.TrimSpace(img.sourcePath)
	if imagePath == "" {
		imagePath = strings.TrimSpace(img.filename)
	}
	if imagePath != "" {
		pathHint := "\n\n[Attached image path: " + imagePath + "]"
		if includeImage {
			pathHint += "\nAn image is attached directly to this message. Prefer native vision understanding first. Only use external image-analysis tools if direct image understanding is unavailable or clearly insufficient."
		} else {
			pathHint += "\nIf direct multimodal image input is unavailable, inspect this local image path with available tools."
		}
		prompt = strings.TrimSpace(prompt + pathHint)
	}

	content := []provider.ContentBlock{provider.TextBlock(prompt)}
	if includeImage {
		content = append(content, provider.ImageBlock(img.img.MIME, image.EncodeBase64(img.img)))
	}
	return content
}

func (m *Model) activeEndpointSupportsVision() bool {
	if m.config == nil {
		return false
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil || resolved == nil {
		return false
	}
	return resolved.SupportsVision
}

// sanitizeAPIError removes API keys from error messages to prevent
// accidental leakage through TUI display, session JSONL, or debug logs.
var apiKeyPatterns = regexp.MustCompile(`(?i)(sk-|Bearer\s+)[\w\-.]{20,}`)

func sanitizeAPIError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	cleaned := apiKeyPatterns.ReplaceAllString(msg, "${1}***")
	if cleaned == msg {
		return err
	}
	return fmt.Errorf("%s", cleaned)
}
