package tui

import (
	"bytes"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// Update handles all Bubble Tea messages and is defined in model_update.go for file-size
// manageability. See model.go for the Model struct definition and other methods.

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle spinner ticks first
	var spinnerCmd tea.Cmd
	if m.spinner.IsActive() {
		spinnerCmd = m.spinner.Update(msg)
	}

	switch msg := msg.(type) {
	case logoMsg:
		m.startupVendor = msg.Vendor
		m.startupEndpoint = msg.Endpoint
		m.startupModel = msg.Model
		m.setActiveRuntimeSelection(msg.Vendor, msg.Endpoint, msg.Model)
		return m, nil

	case tea.WindowSizeMsg:
		m.handleResize(msg.Width, msg.Height)
		// Reset the startup clock when Bubble Tea sends the first WindowSizeMsg.
		// This ensures the startup input gate window is measured from the moment
		// the TUI event loop actually starts, not from model creation time (which
		// can be hundreds of milliseconds earlier due to config loading, IM setup, etc.).
		if m.startedAt.IsZero() || time.Since(m.startedAt) > startupInputGateWindow {
			m.startedAt = time.Now()
		}
		return m, nil

	case imRuntimeUpdatedMsg:
		return m, nil

	case tea.MouseWheelMsg:
		// Route mouse wheel to the active panel's viewport if one is open.
		// MouseWheelMsg implements the MouseMsg interface, so it must appear
		// BEFORE case tea.MouseMsg in this type switch to be matched here.
		if m.fileBrowser != nil && m.fileBrowser.preview != nil {
			if msg.Button == tea.MouseWheelUp {
				m.fileBrowser.preview.viewport.ScrollUp(3)
			} else {
				m.fileBrowser.preview.viewport.ScrollDown(3)
			}
			return m, nil
		}
		if m.previewPanel != nil {
			if msg.Button == tea.MouseWheelUp {
				m.previewPanel.viewport.ScrollUp(3)
			} else {
				m.previewPanel.viewport.ScrollDown(3)
			}
			return m, nil
		}
		// Default: scroll the main conversation.
		if m.chatList != nil && m.chatList.Len() > 0 {
			if msg.Button == tea.MouseWheelUp {
				m.chatList.ScrollUp(3)
			} else {
				m.chatList.ScrollDown(3)
			}
		}
		return m, nil

	case tea.MouseMsg:
		if m.fileBrowser != nil {
			return m.handleFileBrowserMouse(msg)
		}
		if m.previewPanel != nil {
			return m.handlePreviewMouse(msg)
		}
		// Option/Alt+mouse: release mouse to terminal for native text selection
		return m, nil

	case tea.PasteMsg:
		return m.handlePaste(msg, spinnerCmd)

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg, spinnerCmd)

	case streamMsg:
		return m.handleStreamMsg(msg, spinnerCmd)

	case compactResultMsg:
		return m.handleCompactResultMsg(msg)

	case remoteRestartMsg:
		m.quitting = true
		m.restartRequested = true
		return m, tea.Quit

	case remoteInboundMsg:
		return m.handleRemoteInbound(msg, spinnerCmd)

	case displaySleepMsg:
		// stdout is dead (display sleep / terminal closed).
		// The stdoutDeadFlag is already set by the health monitor.
		// No action needed here — the renderer checks IsStdoutDead().
		return m, nil

	case displayWakeMsg:
		// stdout recovered — force a full redraw to refresh stale content.
		return m, nil

	case agentStreamMsg:
		return m.handleAgentStreamMsg(msg, spinnerCmd)

	case agentReasoningMsg:
		return m.handleAgentReasoningMsg(msg, spinnerCmd)

	case agentReasoningDoneMsg:
		m.chatFinishReasoning()
		return m, spinnerCmd

	case agentInterruptMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.chatWriteUser(nextChatID(), msg.Text)
		m.chatWriteSystem(nextSystemID(), m.t("interrupt.delivered"))
		m.chatListScrollToBottom()
		return m, nil

	case webchatUserMsg:
		// Webchat message injected from the webui. Handle like user input.
		text := msg.Text
		if text == "" {
			return m, nil
		}
		// Notify Knight idle timer — webchat counts as user activity too.
		if m.knight != nil {
			m.knight.NotifyActivity()
		}
		// If agent is idle, start a new run with the webchat message
		if m.cancelFunc == nil {
			// Render the user bubble and persist to session.
			m.chatWriteUser(nextChatID(), text)
			m.chatListScrollToBottom()
			m.appendUserMessage(text)
			m.streamBuffer = &bytes.Buffer{}
			m.shellBuffer = nil
			m.streamPrefixWritten = false
			m.loading = true
			m.loopStart = time.Now()
			m.statusActivity = m.t("status.thinking")
			m.statusToolName = ""
			m.statusToolArg = ""
			m.statusToolCount = 0
			cmd := m.startAgent(text)
			return m, tea.Batch(m.startLoadingSpinner(m.statusActivity), cmd)
		}
		// Agent is busy — persist to session, queue for submission.
		// queuePendingSubmission will render the user bubble.
		m.appendUserMessage(text)
		m.queuePendingSubmission(text)
		return m, nil

	case webuiReadyMsg:
		// Display webui URL as a subtle system message in the chat area
		if msg.Addr != "" {
			url := "http://" + msg.Addr
			if msg.Token != "" {
				url += "#token=" + msg.Token
			}
			m.chatWriteSystem(nextSystemID(), "\u2B21 WebUI: "+url)
			m.chatListScrollToBottom()
		}
		return m, nil

	case knightStartupHintMsg:
		if msg.Hint != "" {
			m.chatWriteSystem(nextSystemID(), msg.Hint)
			m.chatListScrollToBottom()
		}
		return m, nil

	case harnessPanelRefreshResultMsg:
		return m.handleHarnessPanelRefreshResultMsg(msg)

	case shellCommandStreamMsg:
		return m.handleShellCommandStreamMsg(msg, spinnerCmd)

	case doneMsg:
		return m.handleDoneMsg(msg)

	case agentDoneMsg:
		return m.handleAgentDoneMsg(msg)

	case shellCommandDoneMsg:
		return m.handleShellCommandDoneMsg(msg)

	case errMsg:
		return m.handleErrMsg(msg)

	case agentErrMsg:
		return m.handleAgentErrMsg(msg)

	case autoRunCheckResultMsg:
		return m.handleAutoRunCheckResultMsg(msg)

	case harnessRunResultMsg:
		return m.handleHarnessRunResultMsg(msg)

	case harnessReviewResultMsg:
		return m.handleHarnessReviewResultMsg(msg)

	case harnessPromoteResultMsg:
		return m.handleHarnessPromoteResultMsg(msg)

	case knightTaskResultMsg:
		return m.handleKnightTaskResultMsg(msg)

	case knightProjectProposalResultMsg:
		return m.handleKnightProjectProposalResultMsg(msg)

	case knightTaskEventMsg:
		return m.handleKnightTaskEventMsg(msg)
	case harnessContextSuggestionsMsg:
		return m.handleHarnessContextSuggestionsMsg(msg)

	case harnessInitResultMsg:
		return m.handleHarnessInitResultMsg(msg)

	case harnessRunProgressMsg:
		return m.handleHarnessRunProgressMsg(msg)

	case harnessPanelAutoRefreshMsg:
		return m.handleHarnessPanelAutoRefreshMsg(msg)

	case startupReadyMsg:
		m.startupBannerVisible = false
		return m, nil

	case projectMemoryLoadedMsg:
		m.projectMemoryLoading = false
		if msg.Err != nil {
			debug.Log("tui", "project memory load failed: %v", msg.Err)
			if m.pendingSubmissionCount() > 0 && !m.loading {
				return m, m.submitPendingSubmissionCmd()
			}
			return m, nil
		}
		m.projMemFiles = append([]string(nil), msg.Files...)
		if m.agent != nil && strings.TrimSpace(msg.Content) != "" {
			m.agent.SetProjectMemoryFiles(msg.Files)
			m.agent.AddMessage(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + msg.Content}},
			})
		}
		if m.pendingSubmissionCount() > 0 && !m.loading {
			if m.shellMode {
				return m, m.submitShellCommand(m.consumePendingSubmission(), false)
			}
			return m, m.submitPendingSubmissionCmd()
		}
		return m, nil

	case ApprovalMsg:
		return m.handleApprovalMsg(msg)

	case DiffConfirmMsg:
		return m.handleDiffConfirmMsg(msg)

	case AskUserMsg:
		return m.handleAskUserMsg(msg)

	case HarnessCheckpointConfirmMsg:
		return m.handleHarnessCheckpointConfirmMsg(msg)

	case subAgentUpdateMsg:
		return m.handleSubAgentUpdateMsg(msg)

	case subAgentTunnelStreamTextMsg:
		return m.handleSubAgentTunnelStreamTextMsg(msg)

	case subAgentTunnelToolCallMsg:
		return m.handleSubAgentTunnelToolCallMsg(msg)

	case subAgentTunnelToolResultMsg:
		return m.handleSubAgentTunnelToolResultMsg(msg)

	case swarmTunnelEventMsg:
		return m.handleSwarmTunnelEventMsg(msg)

	case subAgentFollowRefreshMsg:
		return m.handleSubAgentFollowRefreshMsg(msg)

	case systemNotifyMsg:
		m.chatWriteSystem(nextSystemID(), msg.Text)
		m.chatListScrollToBottom()
		return m, nil

	case followGraceTickMsg:
		return m.handleFollowGraceTickMsg(msg)

	case subAgentDoneMsg:
		return m.handleSubAgentDoneMsg(msg)
	case modeChangeMsg:
		m.mode = msg.Mode
		return m, nil

	case cronPromptMsg:
		sysMsg := m.t("cron.firing")
		m.suppressNextTunnelSystem = sysMsg
		m.chatWriteSystem(nextSystemID(), sysMsg)
		m.emitIMText(sysMsg)
		override := tunnel.MessageData{
			Text:        msg.Prompt,
			DisplayText: sysMsg,
			Kind:        "cron",
		}
		// If agent is idle, submit the cron prompt immediately.
		// Otherwise queue it for processing after the current run finishes.
		if !m.loading {
			m.setNextTunnelUserMessageOverride(override)
			return m, m.submitText(msg.Prompt, true)
		}
		m.queuePendingSubmissionHiddenWithOverride(msg.Prompt, &override)
		return m, nil

	case skillsChangedMsg:
		m.refreshCommands()
		return m, nil

	case updateCheckResultMsg:
		m.applyUpdateCheckResult(msg)
		return m, nil

	case updateCheckTickMsg:
		return m, tea.Batch(m.checkForUpdateCmd(), m.scheduleUpdateCheckCmd())

	case gitBranchTickMsg:
		m.refreshCachedGitBranch()
		return m, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
			return gitBranchTickMsg{}
		})

	case imPanelRefreshMsg:
		return m.handleImPanelRefreshMsg(msg)

	case updatePrepareResultMsg:
		return m.handlePreparedUpdate(msg)

	case toolStatusMsg:
		return m.handleToolStatusMsg(msg, spinnerCmd)

	case agentToolBatchMsg:
		return m.handleAgentToolBatchMsg(msg, spinnerCmd)

	case agentToolStatusMsg:
		return m.handleAgentToolStatusMsg(msg, spinnerCmd)

	case mcpServersMsg:
		return m.handleMcpServersMsg(msg)

	case mcpInstallResultMsg:
		return m.handleMcpInstallResultMsg(msg)

	case tunnelStartMsg:
		return m.handleTunnelStartMsg(msg)

	case tunnelStopMsg:
		m.chatWriteSystem(nextSystemID(), m.t("tunnel.stopped"))
		m.chatListScrollToBottom()
		return m, nil

	case tunnelInboundMsg:
		return m.handleTunnelInboundMsg(msg)

	case tunnelClientConnectedMsg:
		return m.handleTunnelClientConnectedMsg()

	case tunnelModeChangeMsg:
		return m.handleTunnelModeChangeMsg(msg)

	case tunnelApprovalResponseMsg:
		return m.handleTunnelApprovalResponse(msg)

	case tunnelAskUserResponseMsg:
		return m.handleTunnelAskUserResponse(msg)

	case tunnelLanguageChangeMsg:
		return m.handleTunnelLanguageChangeMsg(msg)
	case tunnelThemeChangeMsg:
		return m.handleTunnelThemeChangeMsg(msg)
	case wechatQRCodeMsg:
		return m.handleWechatQRCodeMsg(msg)
	case wechatQRPollMsg:
		return m.handleWechatQRPollMsg(msg)

	case qqBindResultMsg:
		return m.handleQqBindResultMsg(msg)

	case imPanelResultMsg:
		return m.handleImPanelResultMsg(msg)

	case feishuBindResultMsg:
		return m.handleFeishuBindResultMsg(msg)

	case slackBindResultMsg:
		return m.handleSlackBindResultMsg(msg)

	case discordBindResultMsg:
		return m.handleDiscordBindResultMsg(msg)

	case whatsappBindResultMsg:
		return m.handleWhatsappBindResultMsg(msg)

	case dingtalkBindResultMsg:
		return m.handleDingtalkBindResultMsg(msg)

	case wecomBindResultMsg:
		return m.handleWecomBindResultMsg(msg)

	case mattermostBindResultMsg:
		return m.handleMattermostBindResultMsg(msg)

	case matrixBindResultMsg:
		return m.handleMatrixBindResultMsg(msg)

	case signalBindResultMsg:
		return m.handleSignalBindResultMsg(msg)

	case signalDaemonCheckMsg:
		return m.handleSignalDaemonCheckMsg(msg)

	case signalQRCodeMsg:
		return m.handleSignalQRCodeMsg(msg)

	case ircBindResultMsg:
		return m.handleIrcBindResultMsg(msg)

	case nostrBindResultMsg:
		return m.handleNostrBindResultMsg(msg)

	case twitchBindResultMsg:
		return m.handleTwitchBindResultMsg(msg)

	case tgBindResultMsg:
		return m.handleTgBindResultMsg(msg)

	case imEditResultMsg:
		return m.handleImEditResultMsg(msg)

	case providerModelsRefreshResultMsg:
		return m.handleProviderModelsRefreshResultMsg(msg)

	case providerAuthStartMsg:
		return m.handleProviderAuthStartMsg(msg)

	case providerAuthResultMsg:
		return m.handleProviderAuthResultMsg(msg)

	case modelPanelRefreshResultMsg:
		return m.handleModelPanelRefreshResultMsg(msg)

	case mcpUninstallResultMsg:
		return m.handleMcpUninstallResultMsg(msg)

	case mcpOAuthStartMsg:
		return m.handleMcpOAuthStartMsg(msg)

	case mcpOAuthResultMsg:
		return m.handleMcpOAuthResultMsg(msg)

	case setProgramMsg:
		debug.Log("tui", "setProgramMsg received, program was nil=%v", m.program == nil)
		m.program = msg.Program
		// Set startedAt for startup gate if not already set.
		if m.startedAt.IsZero() {
			m.startedAt = time.Now()
		}
		// Clear any terminal response garbage that leaked into the input
		// field before we had a chance to set up the drain guard.
		// Only clear when the content looks like terminal response fragments
		// (contains ;, :, /, digits etc.) to avoid wiping legitimate input
		// set programmatically by callers (e.g. IM tests).
		if val := m.input.Value(); val != "" && looksLikeStartupGarbage(val) {
			debug.Log("tui", "clearing pre-drain input garbage: %q", util.Truncate(val, 80))
			m.input.Reset()
		}
		// Start the input drain window. Terminal responses (OSC 11 color
		// query, CPR, Kitty mode report, mouse-mode/altscreen ACKs) arrive
		// as individual KeyPressMsg events that are indistinguishable from
		// real typing. We suppress all keyboard input until inputDrainEndMsg
		// arrives. The window is intentionally generous (~250ms) because
		// some terminals (and especially when re-running the binary right
		// after a build, with leftover sequences from the previous process
		// in the input buffer) take longer than 50ms to settle.
		m.inputDrainUntil = time.Now().Add(250 * time.Millisecond)
		return m, tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
			return inputDrainEndMsg{}
		})

	case inputDrainEndMsg:
		m.inputDrainUntil = time.Time{} // zero = drain ended
		m.inputReady = true
		debug.Log("tui", "input drain ended, input ready")
		return m, nil

	case imageAttachedMsg:
		m.setComposerImagePlaceholder(msg)
		m.pendingImage = &msg
		return m, nil

	case statusMsg:
		if m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		m.pushTunnelCurrentActivity()
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentStatusMsg:
		if msg.RunID != m.activeAgentRunID || m.runCanceled || !m.loading {
			return m, nil
		}
		m.statusActivity = msg.Activity
		m.statusToolName = msg.ToolName
		m.statusToolArg = msg.ToolArg
		if msg.ToolCount > 0 {
			m.statusToolCount = msg.ToolCount
		}
		m.pushTunnelCurrentActivity()
		return m, combineCmds(spinnerCmd, m.ensureLoadingSpinner(m.statusActivity))

	case agentRoundProgressMsg:
		return m, nil

	case agentRoundSummaryMsg:
		if msg.RunID != m.activeAgentRunID {
			return m, nil
		}
		m.emitIMRoundSummary(msg.Text, msg.ToolCalls, msg.ToolSuccesses, msg.ToolFailures)
		return m, nil

	case agentAskUserMsg:
		// Don't emit IM here — AskUserMsg handler will emit the first question
		// after creating the questionnaire state.
		return m, nil

	}

	// Skip spinnerMsg — it fires every tick and would flood the log.
	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		debug.Log("tui", "CATCHALL msg=%T value=%q", msg, fmt.Sprintf("%+v", msg))
	}
	keyMsg, isKeyPress := msg.(tea.KeyPressMsg)
	if !isKeyPress {
		// Non-keyboard messages still need to reach the textinput so its
		// virtual cursor can process blink scheduling messages
		// (cursor.initialBlinkMsg / cursor.BlinkMsg). Without this the
		// composer cursor never blinks and, depending on the cursor's
		// initial IsBlinked state, may not be visible at all.
		// textinput.Update ignores message types it doesn't handle, so this
		// forward is safe — the input value is only mutated on
		// KeyPressMsg/PasteMsg, which take dedicated branches earlier in
		// this Update function.
		var fwdCmd tea.Cmd
		m.input, fwdCmd = m.input.Update(msg)
		return m, combineCmds(spinnerCmd, fwdCmd)
	}
	var cmd tea.Cmd
	// During startup input drain, suppress all keyboard input.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		debug.Log("tui", "CATCHALL dropped (input drain) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}
	// Before inputReady, discard all keyboard input (same reason as KeyPressMsg handler).
	if !m.inputReady {
		debug.Log("tui", "CATCHALL dropped (not ready) key=%q text=%q", keyMsg.String(), keyMsg.Text)
		return m, spinnerCmd
	}

	oldValue := m.input.Value()
	m.input, cmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		debug.Log("tui", "CATCHALL input changed old=%q new=%q", util.Truncate(oldValue, 80), util.Truncate(newValue, 80))
	}

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	// Clear input hint when user types
	if oldValue != newValue {
		m.inputHint = ""
	}

	return m, combineCmds(spinnerCmd, cmd)
}

// parseApprovalReply parses an IM text reply as an approval decision.
// Returns (decision, true) if the text is a valid approval response.
// Returns (Deny, false) for unrecognized text.
func parseApprovalReply(text string) (permission.Decision, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "y", "yes", "ok", "好", "好的", "允许", "同意", "确认":
		return permission.Allow, true
	case "a", "always", "总是允许", "总是", "始终允许":
		return permission.Allow, true
	case "n", "no", "nope", "拒绝", "取消", "不要", "deny":
		return permission.Deny, true
	}
	// Single-word prefix match: "y xxx" → allow
	if strings.HasPrefix(t, "y") && len(t) <= 3 {
		return permission.Allow, true
	}
	if strings.HasPrefix(t, "n") && len(t) <= 3 {
		return permission.Deny, true
	}
	return permission.Deny, false
}

// isApprovalAlwaysReply returns true if the IM text indicates "always allow".
func isApprovalAlwaysReply(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "a", "always", "总是允许", "总是", "始终允许":
		return true
	}
	return false
}
