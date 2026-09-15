package tui

import (
	"bytes"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Update handles all Bubble Tea messages and is defined in model_update.go for file-size
// manageability. See model.go for the Model struct definition and other methods.

func (m Model) Update(msg tea.Msg) (model tea.Model, cmd tea.Cmd) {
	defer func() {
		next, ok := model.(Model)
		if !ok {
			return
		}
		model, cmd = next.withTerminalTitleCmd(cmd)
	}()

	m.syncAsyncStateCaches()
	model = m
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
		return m.handleWindowSizeMsg(msg)

	case imRuntimeUpdatedMsg:
		return m, nil

	case inspectorItemsLoadedMsg:
		return m.handleInspectorItemsLoaded(msg)

	case tmuxStartupSetupMsg:
		if m.tmuxAvailable() {
			m.setupTmuxLayout(msg.Layout)
		}
		return m, nil

	case providerChangedMsg:
		// Config tool changed provider — refresh model state from config.
		if m.config != nil {
			if resolved, err := m.config.ResolveActiveEndpoint(); err == nil && resolved != nil {
				m.setActiveRuntimeSelection(resolved.VendorName, resolved.EndpointName, resolved.Model)
			}
			m.syncSessionSelection()
		}
		return m, nil

	case mcpServersUpdatedMsg:
		m.applyMCPServersUpdate(msg)
		return m, nil

	case usageInfoUpdatedMsg:
		return m.handleUsageInfoUpdated(msg)

	case systemMsg:
		m.chatWriteSystem(nextSystemID(), msg.msg)
		return m, nil

	case a2aEventUpdatedMsg:
		return m, nil

	case tea.MouseWheelMsg:
		// Route mouse wheel to the active panel's viewport if the open panel
		// has one (the stats panel). Panels without a scrollable viewport
		// fall through to scrolling the main conversation. MouseWheelMsg
		// implements the MouseMsg interface, so it must appear BEFORE case
		// tea.MouseMsg in this type switch to be matched here.
		if vp := m.activePanelViewport(); vp != nil {
			if msg.Button == tea.MouseWheelUp {
				vp.ScrollUp(3)
			} else {
				vp.ScrollDown(3)
			}
			return m, nil
		}
		if m.chatList != nil && m.chatList.Len() > 0 {
			if msg.Button == tea.MouseWheelUp {
				m.chatList.ScrollUp(3)
			} else {
				m.chatList.ScrollDown(3)
			}
		}
		return m, nil

	case tea.MouseMsg:
		// Option/Alt+mouse: release mouse to terminal for native text selection
		return m, nil

	case tea.PasteMsg:
		return m.handlePaste(msg, spinnerCmd)

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg, spinnerCmd)

	case petAnimMsg:
		return m.handlePetAnimMsg(msg)

	case streamMsg:
		return m.handleStreamMsg(msg, spinnerCmd)

	case reviewReadyMsg:
		// #1744 case 3: the async git subprocess takes seconds - an agent run
		// starting (or a /commit while busy, which is whitelisted) in that
		// window made this handler startAgent CONCURRENTLY, overwriting
		// cancelFunc. Queue behind the run instead.
		if m.loading {
			m.queuePendingSubmission("/review")
			return m, nil
		}
		// The /review command prepared the full prompt text; start the agent with it.
		m.chatWriteUser(nextChatID(), "/review")
		m.appendUserMessage("/review")
		m.streamBuffer = &bytes.Buffer{}
		m.streamPrefixWritten = false
		m.setLoading(true)
		m.loopStart = time.Now()
		m.statusActivity = m.t("status.thinking")
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		return m, m.startAgent(msg.text)

	case commitReadyMsg:
		// #1744 case 3: same async-window guard as reviewReadyMsg - /commit
		// is whitelisted to RUN while busy, so its ready message can land
		// mid-run and must queue, not start concurrently.
		if m.loading {
			m.queuePendingSubmission("/commit")
			return m, nil
		}
		// The /commit command prepared the full prompt text; start the agent with it.
		m.chatWriteUser(nextChatID(), "/commit")
		m.appendUserMessage("/commit")
		m.streamBuffer = &bytes.Buffer{}
		m.streamPrefixWritten = false
		m.setLoading(true)
		m.loopStart = time.Now()
		m.statusActivity = m.t("status.thinking")
		m.statusToolName = ""
		m.statusToolArg = ""
		m.statusToolCount = 0
		return m, m.startAgent(msg.text)

	case compactResultMsg:
		return m.handleCompactResultMsg(msg)

	case sessionResumeLoadedMsg:
		return m.handleSessionResumeLoaded(msg)

	case sessionUsageMsg:
		m.recordSessionUsage(msg.Usage, msg.Source)
		return m, nil

	case sessionMetricMsg:
		m.recordSessionMetric(msg.Metric)
		return m, nil

	case tunnelPublishCurrentSessionMsg:
		m.publishTunnelSnapshotForCurrentSession(msg.reset)
		return m, nil

	case armRestartMsg:
		return m.handleArmRestartMsg(msg)

	case restartFallbackMsg:
		return m.handleRestartFallbackMsg(msg)

	case remoteRestartMsg:
		return m.handleRemoteRestartMsg(msg)

	case remoteInboundMsg:
		return m.handleRemoteInbound(msg, spinnerCmd)

	case displaySleepMsg:
		// stdout is dead (display sleep / terminal closed). The flag is
		// exported via IsStdoutDead() for callers that probe the UI; the
		// stock bubbletea renderer lives inside the library, so there is
		// nothing renderable to do while the fd is dead - writes are
		// meaningless until recovery (displayWakeMsg does the work).
		return m, nil

	case displayWakeMsg:
		// stdout recovered - force a full redraw. While dead, every frame
		// the stock renderer attempted (and dropped) left the terminal's
		// visible state arbitrarily stale; ClearScreen discards it and the
		// next View() repaints from scratch. This is the actual consumer
		// half of the monitor: without it, recovery shows corrupted output.
		return m, tea.ClearScreen

	case agentStreamMsg:
		m.noteTurnActivity()
		return m.handleAgentStreamMsg(msg, spinnerCmd)

	case agentReasoningMsg:
		m.noteTurnActivity()
		return m.handleAgentReasoningMsg(msg, spinnerCmd)

	case agentTurnDoneMsg:
		// LLM turn boundary: collapse reasoning, finalize the assistant item,
		// and reset stream state so the next LLM turn creates a fresh
		// assistant item with its own reasoning/text.
		m.chatFinishReasoning()
		m.chatFinishAssistant(m.currentAssistantID())
		m.streamPrefixWritten = false
		m.reasoningActive = false
		// CRITICAL: reset streamBuffer so next turn's text doesn't accumulate
		// on top of the previous turn's content.
		if m.streamBuffer != nil {
			m.streamBuffer.Reset()
		}
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
		return m.handleWebchatUserMsg(msg)

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

	case configMutationMsg:
		// #1367 family root: config writes from Cmd goroutines execute
		// HERE on the Update loop - see config_mutation.go.
		return m.handleConfigMutationMsg(msg)

	case pcResultMsg:
		// #1386-A: pcResultMsg had NO Update case - every PC panel
		// operation result (create/QR/renew/close/bind) was silently
		// dropped by bubbletea: QR never shown, success/error feedback
		// never updated. Routed to the panel fields like signal's results.
		if m.pcPanel != nil {
			if msg.err != nil {
				m.pcPanel.message = fmt.Sprintf("Error: %v", msg.err)
				m.pcPanel.showQR = false
			} else {
				m.pcPanel.message = msg.message
				m.pcPanel.showQR = msg.showQR
				m.pcPanel.qrCode = msg.qrCode
				if msg.inviteURI != "" {
					m.pcPanel.inviteURI = msg.inviteURI
				}
			}
		}
		return m, nil

	case restorePendingImagesMsg:
		// #1393-A: the aborted run's captured attachments come home.
		if msg.RunID != m.activeAgentRunID {
			return m, nil // a newer run started; don't interleave its state
		}
		m.pendingImages = append(m.pendingImages, msg.Images...)
		debug.Log("tui", "restored %d pending image(s) from aborted submit (expand failure)", len(msg.Images))
		return m, nil

	case blindSpotRetryMsg:
		return m, m.handleBlindSpotRetryMsg(msg)

	case knightTaskResultMsg:
		// #902: empty case left the spinner forever and agentBusy stuck —
		// every later submission queued behind a dead /knight run.
		m.setLoading(false)
		if msg.Err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s failed: %v", msg.Result.TaskName, msg.Err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight task %s done (%s): %s", msg.Result.TaskName, msg.Result.Duration, msg.Result.Output))
		}
		return m, nil

	case knightProjectProposalResultMsg:
		// #902: same deadlock class as knightTaskResultMsg.
		m.setLoading(false)
		if msg.Err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal failed: %v", msg.Err))
		} else {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight proposal ready: %s", msg.Proposal.Title))
		}
		return m, nil

	case knightTaskEventMsg:
		// #902: per model_messages.go these should surface as a system chat
		// message (task started/completed progress).
		// #1890: the START event used to setLoading(false) - a scheduled task
		// never set loading, so this actively CLEARED a loading state owned
		// by something else. Only completion touches loading now.
		if msg.Report != "" {
			if m.knightRunning > 0 {
				m.knightRunning--
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight %s: %s", msg.TaskName, msg.Report))
		} else {
			m.knightRunning++
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("knight %s started", msg.TaskName))
		}
		return m, nil

	case initPromptCheckMsg:
		m.initPromptActive = msg.needsInit
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
				Content: []provider.ContentBlock{{Type: "text", Text: msg.Content}},
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

	case inputBellMsg:
		return m.handleInputBellMsg(msg)

	case subAgentUpdateMsg:
		return m.handleSubAgentUpdateMsg(msg)

	case subAgentSystemMsg:
		return m.handleSubAgentSystemMsg(msg)

	case subAgentTunnelStreamTextMsg:
		return m.handleSubAgentTunnelStreamTextMsg(msg)

	case subAgentTunnelReasoningMsg:
		return m.handleSubAgentTunnelReasoningMsg(msg)

	case subAgentTunnelToolCallMsg:
		return m.handleSubAgentTunnelToolCallMsg(msg)

	case subAgentTunnelToolResultMsg:
		return m.handleSubAgentTunnelToolResultMsg(msg)

	case swarmTunnelEventMsg:
		return m.handleSwarmTunnelEventMsg(msg)

	case subAgentFollowRefreshMsg:
		return m.handleSubAgentFollowRefreshMsg(msg)

	case systemNotifyMsg:
		if msg.ItemID != "" {
			if item := m.chatList.FindByID(msg.ItemID); item != nil {
				if sys, ok := item.(*chat.SystemItem); ok {
					if msg.Replace {
						sys.SetText(msg.Text)
					} else {
						sys.AppendText(msg.Text)
					}
					m.chatListScrollToBottom()
					return m, nil
				}
			}
			// Item not found yet — create it with the provided ItemID so
			// subsequent retries with the same ItemID can find and append.
			m.chatWriteSystem(msg.ItemID, msg.Text)
			m.chatListFollowOutput()
			return m, nil
		}
		m.chatWriteSystem(nextSystemID(), msg.Text)
		m.chatListFollowOutput()
		return m, nil

	case followGraceTickMsg:
		return m.handleFollowGraceTickMsg(msg)

	case subAgentDoneMsg:
		return m.handleSubAgentDoneMsg(msg)

	case subAgentsCancelDoneMsg:
		debug.Log("cancel", "subAgentsCancelDoneMsg received: clearing subAgentsCanceling=false")
		m.subAgentsCanceling = false
		return m, nil

	case modeChangeMsg:
		m.mode = msg.Mode
		return m, nil

	case cronPromptMsg:
		return m.handleCronPromptMsg(msg)

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
		m.noteTurnActivity()
		return m.handleAgentToolBatchMsg(msg, spinnerCmd)

	case agentToolStatusMsg:
		m.noteTurnActivity()
		return m.handleAgentToolStatusMsg(msg, spinnerCmd)

	case mcpServersMsg:
		return m.handleMcpServersMsg(msg)

	case mcpInstallResultMsg:
		return m.handleMcpInstallResultMsg(msg)

	case tunnelStartMsg:
		return m.handleTunnelStartMsg(msg)

	case tunnelRefreshMsg:
		return m.handleTunnelRefreshMsg(msg)

	case tunnelShareBootstrapMsg:
		return m.handleTunnelShareBootstrapMsg(msg)

	case tunnelStopMsg:
		m.chatWriteSystem(nextSystemID(), m.t("tunnel.stopped"))
		m.chatListScrollToBottom()
		return m, nil

	case tunnelInboundMsg:
		return m.handleTunnelInboundMsg(msg)

	case tunnelClientConnectedMsg:
		return m.handleTunnelClientConnectedMsgForGeneration(msg.generation)

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
	case wechatPollDueMsg:
		// #1792 case 2: the paced re-poll fired - run the real poll.
		if m.wechatPanel != nil {
			return m, m.pollWechatQRStatus(msg.token)
		}
		return m, nil

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

	case mcpHealthCheckTickMsg:
		return m.handleMcpHealthCheckTick(msg)

	case setProgramMsg:
		return m.handleSetProgramMsg(msg)

	case inputDrainEndMsg:
		return m.handleInputDrainEndMsg(msg)

	case imageAttachedMsg:
		return m.handleImageAttachedMsg(msg)

	case textPasteMsg:
		return m.handleTextPasteMsg(msg)

	case statusMsg:
		return m.handleStatusMsg(msg, spinnerCmd)

	case agentStatusMsg:
		return m.handleAgentStatusMsg(msg, spinnerCmd)

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

	// ---- Async verification messages ----
	case verifyProgressMsg:
		m.chatWriteSystem(nextSystemID(), msg.text)
		m.chatListFollowOutput()
		return m, nil

	case toolProgressMsg:
		// Update the running tool's output in-place for a streaming effect.
		m.noteTurnActivity() // streaming tool output is turn activity (#375)
		if msg.toolID != "" {
			m.chatUpdateToolOutput(msg.toolID, msg.output)
		}
		m.chatListFollowOutput()
		return m, nil

	case verifyResultMsg:
		if msg.result.Passed {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("✅ [Verification passed: `%s`]", msg.result.Command))
		} else {
			output := msg.result.Output
			if len(output) > 500 {
				output = util.Truncate(output, 500) + "…"
			}
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("❌ [Verification failed: `%s`]\n```\n%s\n```", msg.result.Command, output))
		}
		m.chatListFollowOutput()
		return m, nil

	// ---- LAN chat messages ----
	case lanchatMsg, lanchatReceiptMsg, lanchatPeerJoinMsg, lanchatPeerLeaveMsg, lanchatApprovalReqMsg, lanchatNickChangeMsg, lanchatAutoApproveMsg:
		return m.handleLanChatPanelUpdate(msg)

	}

	// Skip spinnerMsg and blinkMsg — they fire every tick and would flood the log.
	if _, isSpinner := msg.(spinnerMsg); !isSpinner {
		msgType := fmt.Sprintf("%T", msg)
		if !strings.Contains(msgType, "Blink") {
			// Don't log every bubbletea internal message — cursor blink alone generates ~160 lines/min
		}
	}
	_, isKeyPress := msg.(tea.KeyPressMsg)
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
	var keyCmd tea.Cmd
	// During startup input drain, suppress all keyboard input.
	if !m.inputDrainUntil.IsZero() && time.Now().Before(m.inputDrainUntil) {
		// Don't log dropped keypresses during input drain
		return m, spinnerCmd
	}
	// Before inputReady, discard all keyboard input (same reason as KeyPressMsg handler).
	if !m.inputReady {
		// Don't log dropped keypresses when not ready
		return m, spinnerCmd
	}

	oldValue := m.input.Value()
	m.input, keyCmd = m.input.Update(msg)
	newValue := m.input.Value()
	if oldValue != newValue {
		// Don't log input field changes
	}

	// Update autocomplete state based on current input
	m.updateAutoComplete()

	// Clear input hint when user types
	if oldValue != newValue {
		m.inputHint = ""
	}

	return m, combineCmds(spinnerCmd, keyCmd)
}
