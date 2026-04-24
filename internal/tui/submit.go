package tui

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/session"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

func (m *Model) appendUserMessage(text string) {
	m.sessionMutex().Lock()
	defer m.sessionMutex().Unlock()
	if m.session == nil || m.sessionStore == nil {
		return
	}
	msg := provider.Message{
		Role:    "user",
		Content: []provider.ContentBlock{{Type: "text", Text: text}},
	}
	// Auto-generate title from first user message
	if m.session.Title == "" || m.session.Title == "New session" {
		if len(text) > 60 {
			m.session.Title = text[:57] + "..."
		} else {
			m.session.Title = text
		}
	}
	if store, ok := m.sessionStore.(*session.JSONLStore); ok {
		_ = store.AppendMessage(m.session, msg)
	} else {
		m.session.Messages = append(m.session.Messages, msg)
		_ = m.sessionStore.Save(m.session)
	}
}

func (m *Model) startAgent(text string) tea.Cmd {
	debug.Log("tui", "startAgent called: text=%s", truncateStr(text, 200))
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
				// Kick off background pre-compact during the user's idle time
				// before notifying the TUI the run is done. If tokens are
				// below threshold this is a cheap no-op.
				if m.agent != nil {
					m.agent.StartPreCompact()
				}
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

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
				if m.agent != nil {
					m.agent.StartPreCompact()
				}
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

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
		status := toolBatchStatus
		tools := toolBatchTools
		toolBatchStatus = nil
		toolBatchTools = nil
		batchMu.Unlock()

		if m.program == nil {
			return
		}
		// Send text first (if any)
		if text != "" {
			m.program.Send(agentStreamMsg{RunID: runID, Text: text})
		}
		// Send all batched tool events in a single message
		if len(status) > 0 || len(tools) > 0 {
			m.program.Send(agentToolBatchMsg{
				RunID:      runID,
				StatusMsgs: status,
				ToolMsgs:   tools,
			})
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
		// Protect the entire stream callback against panics. The callback
		// runs on the provider's stream-reading goroutine; an unrecovered
		// panic here would terminate the read goroutine (best case) or
		// crash the whole process (worst case). Recovering keeps the TUI
		// alive and lets the higher-level error handling surface a sane
		// error to the user.
		defer safego.Recover("tui.streamCallback")
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
		case provider.StreamEventToolCallDone:
			// Flush any pending text before tool events to keep output ordering correct.
			flushBatch()
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			debug.Log("tool-header", "[ToolCallDone] tool=%s DisplayName=%q Detail=%q", event.Tool.Name, present.DisplayName, present.Detail)
			if event.Tool.Name == "ask_user" {
				round.SetAskUser(m.formatIMAskUserPrompt(string(event.Tool.Arguments)))
			}
			if isSubAgentLifecycleTool(event.Tool.Name) {
				// Sub-agent lifecycle tools are low-frequency; send directly.
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:   event.Tool.ID,
					ToolName: event.Tool.Name,
					Activity: m.t("status.thinking"),
					Running:  true,
					RawArgs:  string(event.Tool.Arguments),
					Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
				}})
				return
			}
			round.ToolCalls++
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
				Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
			}})
			batchMu.Unlock()
		case provider.StreamEventToolResult:
			// Flush any pending text before tool results.
			flushBatch()
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			debug.Log("tool-header", "[ToolResult] tool=%s DisplayName=%q Detail=%q", event.Tool.Name, present.DisplayName, present.Detail)
			if isSubAgentLifecycleTool(event.Tool.Name) {
				// Sub-agent lifecycle tools are low-frequency; send directly.
				m.program.Send(agentToolStatusMsg{RunID: runID, ToolStatusMsg: ToolStatusMsg{
					ToolID:   event.Tool.ID,
					ToolName: event.Tool.Name,
					Activity: m.t("status.thinking"),
					Running:  false,
					Result:   event.Result,
					RawArgs:  string(event.Tool.Arguments),
					Args:     truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
					IsError:  event.IsError,
				}})
				m.program.Send(subAgentUpdateMsg{})
				return
			}
			if event.IsError {
				round.ToolFailures++
			} else {
				round.ToolSuccesses++
			}
			m.emitIMEvent(im.OutboundEvent{
				Kind: im.OutboundEventToolResult,
				ToolRes: &im.ToolResultInfo{
					ToolName: event.Tool.Name,
					Args:     string(event.Tool.Arguments),
					Result:   event.Result,
					IsError:  event.IsError,
					Detail:   present.Detail,
				},
			})
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
				Args:        truncateString(compactToolArgsPreview(string(event.Tool.Arguments)), 100),
				IsError:     event.IsError,
			}})
			batchMu.Unlock()
		case provider.StreamEventError:
			if !errors.Is(event.Error, context.Canceled) {
				streamErrSent = true
				m.program.Send(agentErrMsg{RunID: runID, Err: event.Error})
			}
		case provider.StreamEventDone:
			// Flush any remaining text/tool events synchronously before
			// processing the Done event. This is critical for multi-round
			// agent loops: each round ends with a Done event, and subsequent
			// rounds may contain pure text with no tool calls. Without this
			// flush, the text would remain stuck in batchBuf because the
			// ticker goroutine exits after the first closeBatchDone().
			flushBatch()
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

type agentIMRoundState struct {
	text          strings.Builder
	ToolCalls     int
	ToolSuccesses int
	ToolFailures  int
	AskUserText   string
}

func (s *agentIMRoundState) AppendText(text string) {
	s.text.WriteString(text)
}

func (s *agentIMRoundState) Text() string {
	return s.text.String()
}

func (s *agentIMRoundState) SetAskUser(text string) {
	s.AskUserText = strings.TrimSpace(text)
}

func (s *agentIMRoundState) HasVisibleOutput() bool {
	return strings.TrimSpace(s.Text()) != ""
}

func (s *agentIMRoundState) Reset() {
	s.text.Reset()
	s.ToolCalls = 0
	s.ToolSuccesses = 0
	s.ToolFailures = 0
	s.AskUserText = ""
}

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
