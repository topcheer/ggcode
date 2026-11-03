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

	"github.com/topcheer/ggcode/internal/agentruntime"
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
		Content: []provider.ContentBlock{{Type: "text", Text: strings.TrimSpace(text)}},
	}

	m.session.Messages = append(m.session.Messages, msg)

	m.persistedMsgCount++
	m.session.UpdatedAt = time.Now()

	if m.session.Title == "" || m.session.Title == "New session" || m.session.Title == "新会话" {
		if smart := agentruntime.GenerateTitle(text); smart != "" {
			m.session.Title = smart
		}
	}

	m.session.Preview = util.Truncate(strings.TrimSpace(text), 120)
	store := m.sessionStore
	m.sessionMutex().Unlock()

	m.pushTunnelUserMessage(text)

	if _, ok := store.(*session.JSONLStore); !ok {

		m.sessionMutex().Lock()
		_ = store.Save(m.session)
		m.sessionMutex().Unlock()
	}
}

func (m *Model) startAgent(text string) tea.Cmd {
	debug.Log("tui", "startAgent called: text=%s", util.Truncate(text, 200))
	m.usageTurnIndex++

	if m.lanChatHub != nil {
		m.lanChatHub.SetAgentBusy(true)
	}

	m.ensureProviderSync()
	m.rebuildSystemPrompt()

	imgs := m.pendingImages
	m.pendingImages = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeAgentRunID++
	runID := m.activeAgentRunID
	if m.agent != nil {
		m.agent.SetInterruptionHandler(func() []provider.ContentBlock {
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
					defer m.agent.StartPreCompact()
				}
				if m.program != nil {
					m.program.Send(agentDoneMsg{RunID: runID})
				}
				cancel()
			}()

			m.pushInitialTunnelRunState()
			if err := m.runAgentSubmission(ctx, runID, text, imgs); err != nil && !errors.Is(err, context.Canceled) && m.program != nil {
				m.program.Send(agentErrMsg{RunID: runID, Err: err})
			}
		})

		return nil
	}
}

func (m *Model) startAgentWithExpand(text string) tea.Cmd {
	m.usageTurnIndex++

	if m.lanChatHub != nil {
		m.lanChatHub.SetAgentBusy(true)
	}
	m.rebuildSystemPrompt()
	m.refreshStatusline()
	m.oscWorking("")
	imgs := m.pendingImages
	m.pendingImages = nil
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.activeAgentRunID++
	runID := m.activeAgentRunID
	if m.agent != nil {
		m.agent.SetInterruptionHandler(func() []provider.ContentBlock {
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

			if m.agent != nil {
				m.agent.StartRunTracking()
			}

			workDir, _ := os.Getwd()
			expandedMsg, expandErr := ExpandMentions(text, workDir)
			if expandErr != nil && m.program != nil {

				m.program.Send(restorePendingImagesMsg{RunID: runID, Images: imgs})
				m.program.Send(agentErrMsg{RunID: runID, Err: expandErr})
				return
			}

			expandedMsg = ExpandURLs(ctx, expandedMsg)

			if err := m.runAgentSubmission(ctx, runID, expandedMsg, imgs); err != nil && !errors.Is(err, context.Canceled) && m.program != nil {
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

func (m *Model) runAgentSubmission(ctx context.Context, runID int, text string, imgs []imageAttachedMsg) error {
	content := buildAgentSubmissionContent(text, imgs, false)
	if len(imgs) == 0 {
		streamErrSent, err := m.runAgentWithContent(ctx, runID, content)
		if streamErrSent && err != nil && !errors.Is(err, context.Canceled) {

			return nil
		}
		return err
	}

	if !m.activeEndpointSupportsVision() {

		if vm := m.temporaryVisionModelForTurn(); vm != "" {
			userModel := m.config.Model
			debug.Log("tui", "vision fallback: turn-scoped switch to %s (user model: %s)", vm, userModel)
			if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, vm); err == nil {

				defer m.restoreUserModelAfterVisionTurn(userModel)
				if err := m.tryActivateCurrentSelection(); err != nil {

					m.restoreUserModelAfterVisionTurn(userModel)
					debug.Log("tui", "vision fallback: activation failed, restored user model: %v", err)
				} else {
					streamErrSent, err := m.runAgentWithContent(ctx, runID, buildAgentSubmissionContent(text, imgs, true))
					if err == nil || errors.Is(err, context.Canceled) {
						return err
					}
					if streamErrSent {
						return nil
					}

					m.restoreUserModelAfterVisionTurn(userModel)
					if !provider.IsImageBlockFallbackCandidate(err) {
						return err
					}
					debug.Log("tui", "vision fallback model rejected request, retrying text-only: %v", err)
					_, retryErr := m.runAgentWithContent(ctx, runID, content)
					if retryErr == nil {
						return nil
					}
					return retryErr
				}
			}
			debug.Log("tui", "vision fallback: no usable switch, proceeding text-only")
		}
		streamErrSent, err := m.runAgentWithContent(ctx, runID, content)
		if streamErrSent && err != nil && !errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	streamErrSent, err := m.runAgentWithContent(ctx, runID, buildAgentSubmissionContent(text, imgs, true))
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	if streamErrSent || !provider.IsImageBlockFallbackCandidate(err) {
		if streamErrSent {
			return nil
		}
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

const streamBatchInterval = 80 * time.Millisecond

func (m *Model) runAgentWithContent(ctx context.Context, runID int, content []provider.ContentBlock) (bool, error) {
	streamErrSent := false
	writingStatusSent := false
	retryItemID := ""
	round := agentIMRoundState{}

	var batchMu sync.Mutex
	var batchBuf strings.Builder
	var batchReasoningBuf strings.Builder
	var fullReasoningBuf strings.Builder
	var toolBatchStatus []agentStatusMsg
	var toolBatchTools []agentToolStatusMsg
	batchDone := make(chan struct{})

	var batchDoneOnce sync.Once
	closeBatchDone := func() {
		batchDoneOnce.Do(func() { close(batchDone) })
	}

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

	safego.Go("tui.streamBatch.ticker", func() {
		ticker := time.NewTicker(streamBatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				flushBatch()
			case <-batchDone:

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

		if m.webuiBridge != nil {
			m.webuiBridge.BroadcastEvent(event)
		}

		m.pushTunnelEvent(event)
		if m.program == nil {
			return
		}
		switch event.Type {
		case provider.StreamEventText:

			round.AppendText(event.Text)

			if m.streamTextStart.IsZero() {
				m.streamTextStart = time.Now()
			}
			m.streamChars += len(event.Text)

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

			flushBatch()
			if retryItemID == "" {
				retryItemID = nextSystemID()
				m.program.Send(systemNotifyMsg{Text: event.Text, ItemID: retryItemID, Replace: true})
			} else {
				m.program.Send(systemNotifyMsg{Text: event.Text, ItemID: retryItemID, Replace: true})
			}
		case provider.StreamEventToolCallDone:

			flushBatch()
			writingStatusSent = false
			retryItemID = ""
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if event.Tool.Name == "ask_user" {
				round.SetAskUser(m.formatIMAskUserPrompt(string(event.Tool.Arguments)))
			}

			if m.imEmitter != nil {
				m.imEmitter.EmitToolStatus(event.Tool.Name, string(event.Tool.Arguments))
			}
			if isSubAgentLifecycleTool(event.Tool.Name) {

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

			flushBatch()
			writingStatusSent = false
			present := describeTool(m.currentLanguage(), event.Tool.Name, string(event.Tool.Arguments))
			if isSubAgentLifecycleTool(event.Tool.Name) {

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

			toolInfo := im.ToolResultInfo{
				ToolName:     event.Tool.Name,
				Args:         string(event.Tool.Arguments),
				Result:       event.Result,
				IsError:      event.IsError,
				Detail:       present.Detail,
				CallNotified: m.imEmitter != nil && im.IsLongRunningTool(event.Tool.Name),
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

			flushBatch()
			retryItemID = ""
			m.program.Send(agentTurnDoneMsg{})
			writingStatusSent = false

			batchMu.Lock()
			fullReasoningBuf.Reset()
			batchMu.Unlock()

			m.streamChars = 0
			m.streamTextStart = time.Time{}
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
	}, "human")

	closeBatchDone()

	if err != nil && !errors.Is(err, context.Canceled) && !streamErrSent {
		return false, err
	}
	return streamErrSent, err
}

func buildBatchedStreamMessages(runID int, text, reasoning string, status []agentStatusMsg, tools []agentToolStatusMsg) []tea.Msg {
	msgs := make([]tea.Msg, 0, 3)

	if reasoning != "" {
		msgs = append(msgs, agentReasoningMsg{RunID: runID, Text: reasoning})
	}
	if text != "" {
		msgs = append(msgs, agentStreamMsg{RunID: runID, Text: text})
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

func buildAgentSubmissionContent(text string, imgs []imageAttachedMsg, includeImage bool) []provider.ContentBlock {
	prompt := strings.TrimSpace(text)
	if len(imgs) == 0 {
		return []provider.ContentBlock{provider.TextBlock(prompt)}
	}

	var pathHints []string
	for _, img := range imgs {
		imagePath := strings.TrimSpace(img.sourcePath)
		if imagePath == "" {
			imagePath = strings.TrimSpace(img.filename)
		}
		if imagePath != "" {
			pathHints = append(pathHints, imagePath)
		}
	}
	if len(pathHints) > 0 {
		pathHint := "\n\n[Attached image path(s): " + strings.Join(pathHints, ", ") + "]"
		if includeImage {
			pathHint += "\nImage(s) are attached directly to this message. Prefer native vision understanding first. Only use external image-analysis tools if direct image understanding is unavailable or clearly insufficient."
		} else {
			pathHint += "\nIf direct multimodal image input is unavailable, inspect these local image paths with available tools."
		}
		prompt = strings.TrimSpace(prompt + pathHint)
	}

	content := []provider.ContentBlock{provider.TextBlock(prompt)}
	if includeImage {
		for _, img := range imgs {
			content = append(content, provider.ImageBlock(img.img.MIME, image.EncodeBase64(img.img)))
		}
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

func (m *Model) temporaryVisionModelForTurn() string {
	if m.config == nil {
		return ""
	}
	return agentruntime.VisionTurnModel(m.config)
}

func (m *Model) restoreUserModelAfterVisionTurn(userModel string) {
	if m.config == nil || userModel == "" || m.config.Model == userModel {
		return
	}
	if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, userModel); err != nil {
		debug.Log("tui", "vision fallback restore: %v", err)
		return
	}
	if err := m.tryActivateCurrentSelection(); err != nil {
		debug.Log("tui", "vision fallback restore activation: %v", err)
	}
}

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
