package tui

// Exact-type dispatch table for Model.Update (#2422, phase 2 of #2357).
//
// The 133-case type switch in model_update.go is replaced by a
// reflect.Type-keyed table. Semantics are identical to a type switch over
// concrete (non-interface) cases: exact dynamic-type match only. The ONLY
// case that cannot live here is tea.MouseMsg, which is an interface type -
// it stays as a residual switch in Update after the table lookup misses.
//
// Every entry preserves the original switch's case order (declaration
// order here mirrors the old top-to-bottom order for reviewability);
// order is irrelevant for correctness because exact-type matching is
// disjoint, which the original switch's structure already guaranteed
// (a concrete case always preceded the interface case that could also
// match it: MouseWheelMsg before tea.MouseMsg).
//
// Zero behavior change - each closure body is the verbatim statement
// sequence of the original inline case.

import (
	"fmt"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/debug"
)

// updateHandler is one table entry: it receives the model, the asserted
// message, and the spinner command the original switch computed up front.
type updateHandler func(m Model, msg tea.Msg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd)

// updateHandlers maps reflect.TypeOf(msg) -> handler. Populated once in
// init() below.
var updateHandlers = map[reflect.Type]updateHandler{}

// regUpdate registers an exact-type dispatch entry whose handler wants
// the spinner command.
func regUpdate[M tea.Msg](h func(m Model, msg M, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd)) {
	updateHandlers[reflect.TypeOf(*new(M))] = func(m Model, msg tea.Msg, sc tea.Cmd) (tea.Model, tea.Cmd) {
		return h(m, msg.(M), sc)
	}
}

// regUpdatePlain registers an exact-type dispatch entry for handlers that
// do not need the spinner command.
func regUpdatePlain[M tea.Msg](h func(m Model, msg M) (tea.Model, tea.Cmd)) {
	regUpdate(func(m Model, msg M, _ tea.Cmd) (tea.Model, tea.Cmd) { return h(m, msg) })
}

// regNoop registers a no-op entry (original case was `return m, nil`).
func regNoop[M tea.Msg]() {
	regUpdatePlain(func(m Model, _ M) (tea.Model, tea.Cmd) { return m, nil })
}

func init() {
	regUpdatePlain(func(m Model, msg logoMsg) (tea.Model, tea.Cmd) { return m.handleLogoMsg(msg) })
	regUpdatePlain(func(m Model, msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) { return m.handleWindowSizeMsg(msg) })
	regNoop[imRuntimeUpdatedMsg]()
	regUpdatePlain(func(m Model, msg inspectorItemsLoadedMsg) (tea.Model, tea.Cmd) {
		return m.handleInspectorItemsLoaded(msg)
	})
	regUpdatePlain(func(m Model, msg tmuxStartupSetupMsg) (tea.Model, tea.Cmd) { return m.handleTmuxStartupSetupMsg(msg) })
	regUpdatePlain(func(m Model, msg providerChangedMsg) (tea.Model, tea.Cmd) { return m.handleProviderChangedMsg(msg) })
	regUpdatePlain(func(m Model, msg mcpServersUpdatedMsg) (tea.Model, tea.Cmd) {
		m.applyMCPServersUpdate(msg)
		return m, nil
	})
	regUpdatePlain(func(m Model, msg usageInfoUpdatedMsg) (tea.Model, tea.Cmd) { return m.handleUsageInfoUpdated(msg) })
	regUpdatePlain(func(m Model, msg usageSidebarRefreshMsg) (tea.Model, tea.Cmd) {
		return m.handleUsageSidebarRefreshMsg()
	})
	regUpdatePlain(func(m Model, msg systemMsg) (tea.Model, tea.Cmd) {
		m.chatWriteSystem(nextSystemID(), msg.msg)
		return m, nil
	})
	regNoop[a2aEventUpdatedMsg]()
	regUpdatePlain(func(m Model, msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) { return m.handleMouseWheelMsg(msg) })
	regUpdate(func(m Model, msg tea.PasteMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handlePaste(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg tea.KeyPressMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleKeyPress(msg, spinnerCmd)
	})
	regUpdatePlain(func(m Model, msg petAnimMsg) (tea.Model, tea.Cmd) { return m.handlePetAnimMsg(msg) })
	regUpdate(func(m Model, msg streamMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleStreamMsg(msg, spinnerCmd)
	})
	regUpdatePlain(func(m Model, msg reviewReadyMsg) (tea.Model, tea.Cmd) { return m.handleReviewReadyMsg(msg) })
	regUpdatePlain(func(m Model, msg commitReadyMsg) (tea.Model, tea.Cmd) { return m.handleCommitReadyMsg(msg) })
	regUpdatePlain(func(m Model, msg compactResultMsg) (tea.Model, tea.Cmd) { return m.handleCompactResultMsg(msg) })
	regUpdatePlain(func(m Model, msg sessionResumeLoadedMsg) (tea.Model, tea.Cmd) {
		return m.handleSessionResumeLoaded(msg)
	})
	regUpdatePlain(func(m Model, msg sessionUsageMsg) (tea.Model, tea.Cmd) {
		m.recordSessionUsage(msg.Usage, msg.Source)
		return m, nil
	})
	regUpdatePlain(func(m Model, msg sessionMetricMsg) (tea.Model, tea.Cmd) {
		m.recordSessionMetric(msg.Metric)
		return m, nil
	})
	regUpdatePlain(func(m Model, msg tunnelPublishCurrentSessionMsg) (tea.Model, tea.Cmd) {
		m.publishTunnelSnapshotForCurrentSession(msg.reset)
		return m, nil
	})
	regUpdatePlain(func(m Model, msg armRestartMsg) (tea.Model, tea.Cmd) { return m.handleArmRestartMsg(msg) })
	regUpdatePlain(func(m Model, msg restartFallbackMsg) (tea.Model, tea.Cmd) { return m.handleRestartFallbackMsg(msg) })
	regUpdatePlain(func(m Model, msg remoteRestartMsg) (tea.Model, tea.Cmd) { return m.handleRemoteRestartMsg(msg) })
	regUpdate(func(m Model, msg remoteInboundMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleRemoteInbound(msg, spinnerCmd)
	})
	// stdout is dead (display sleep / terminal closed). Nothing renderable
	// to do while the fd is dead - displayWakeMsg does the recovery work.
	regNoop[displaySleepMsg]()
	regUpdatePlain(func(m Model, msg displayWakeMsg) (tea.Model, tea.Cmd) {
		// stdout recovered - force a full redraw; ClearScreen discards the
		// terminal's arbitrarily stale visible state.
		return m, tea.ClearScreen
	})
	regUpdate(func(m Model, msg agentStreamMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.noteTurnActivity()
		return m.handleAgentStreamMsg(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg agentReasoningMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.noteTurnActivity()
		return m.handleAgentReasoningMsg(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg agentTurnDoneMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleAgentTurnDoneMsg(msg, spinnerCmd)
	})
	regUpdatePlain(func(m Model, msg agentInterruptMsg) (tea.Model, tea.Cmd) { return m.handleAgentInterruptMsg(msg) })
	regUpdatePlain(func(m Model, msg webchatUserMsg) (tea.Model, tea.Cmd) { return m.handleWebchatUserMsg(msg) })
	regUpdatePlain(func(m Model, msg webuiReadyMsg) (tea.Model, tea.Cmd) { return m.handleWebuiReadyMsg(msg) })
	regUpdatePlain(func(m Model, msg knightStartupHintMsg) (tea.Model, tea.Cmd) { return m.handleKnightStartupHintMsg(msg) })
	regUpdate(func(m Model, msg shellCommandStreamMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleShellCommandStreamMsg(msg, spinnerCmd)
	})
	regUpdatePlain(func(m Model, msg doneMsg) (tea.Model, tea.Cmd) { return m.handleDoneMsg(msg) })
	regUpdatePlain(func(m Model, msg agentDoneMsg) (tea.Model, tea.Cmd) { return m.handleAgentDoneMsg(msg) })
	regUpdatePlain(func(m Model, msg shellCommandDoneMsg) (tea.Model, tea.Cmd) { return m.handleShellCommandDoneMsg(msg) })
	regUpdatePlain(func(m Model, msg errMsg) (tea.Model, tea.Cmd) { return m.handleErrMsg(msg) })
	regUpdatePlain(func(m Model, msg agentErrMsg) (tea.Model, tea.Cmd) { return m.handleAgentErrMsg(msg) })
	regUpdatePlain(func(m Model, msg configMutationMsg) (tea.Model, tea.Cmd) { return m.handleConfigMutationMsg(msg) })
	regUpdatePlain(func(m Model, msg pcResultMsg) (tea.Model, tea.Cmd) { return m.handlePcResultMsg(msg) })
	regUpdatePlain(func(m Model, msg restorePendingImagesMsg) (tea.Model, tea.Cmd) {
		return m.handleRestorePendingImagesMsg(msg)
	})
	regUpdatePlain(func(m Model, msg knightTaskResultMsg) (tea.Model, tea.Cmd) { return m.handleKnightTaskResultMsg(msg) })
	regUpdatePlain(func(m Model, msg knightProjectProposalResultMsg) (tea.Model, tea.Cmd) {
		return m.handleKnightProjectProposalResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg knightTaskEventMsg) (tea.Model, tea.Cmd) { return m.handleKnightTaskEventMsg(msg) })
	regUpdatePlain(func(m Model, msg initPromptCheckMsg) (tea.Model, tea.Cmd) {
		m.initPromptActive = msg.needsInit
		return m, nil
	})
	regUpdatePlain(func(m Model, msg projectMemoryLoadedMsg) (tea.Model, tea.Cmd) {
		return m.handleProjectMemoryLoadedMsg(msg)
	})
	regUpdatePlain(func(m Model, msg ApprovalMsg) (tea.Model, tea.Cmd) { return m.handleApprovalMsg(msg) })
	regUpdatePlain(func(m Model, msg DiffConfirmMsg) (tea.Model, tea.Cmd) { return m.handleDiffConfirmMsg(msg) })
	regUpdatePlain(func(m Model, msg AskUserMsg) (tea.Model, tea.Cmd) { return m.handleAskUserMsg(msg) })
	regUpdatePlain(func(m Model, msg inputBellMsg) (tea.Model, tea.Cmd) { return m.handleInputBellMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentUpdateMsg) (tea.Model, tea.Cmd) { return m.handleSubAgentUpdateMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentSystemMsg) (tea.Model, tea.Cmd) { return m.handleSubAgentSystemMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentTunnelStreamTextMsg) (tea.Model, tea.Cmd) {
		return m.handleSubAgentTunnelStreamTextMsg(msg)
	})
	regUpdatePlain(func(m Model, msg subAgentTunnelReasoningMsg) (tea.Model, tea.Cmd) {
		return m.handleSubAgentTunnelReasoningMsg(msg)
	})
	regUpdatePlain(func(m Model, msg subAgentTunnelToolCallMsg) (tea.Model, tea.Cmd) {
		return m.handleSubAgentTunnelToolCallMsg(msg)
	})
	regUpdatePlain(func(m Model, msg subAgentTunnelToolResultMsg) (tea.Model, tea.Cmd) {
		return m.handleSubAgentTunnelToolResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg swarmTunnelEventMsg) (tea.Model, tea.Cmd) { return m.handleSwarmTunnelEventMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentFollowRefreshMsg) (tea.Model, tea.Cmd) {
		return m.handleSubAgentFollowRefreshMsg(msg)
	})
	regUpdatePlain(func(m Model, msg systemNotifyMsg) (tea.Model, tea.Cmd) { return m.handleSystemNotifyMsg(msg) })
	regUpdatePlain(func(m Model, msg followGraceTickMsg) (tea.Model, tea.Cmd) { return m.handleFollowGraceTickMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentDoneMsg) (tea.Model, tea.Cmd) { return m.handleSubAgentDoneMsg(msg) })
	regUpdatePlain(func(m Model, msg subAgentsCancelDoneMsg) (tea.Model, tea.Cmd) {
		debug.Log("cancel", "subAgentsCancelDoneMsg received: clearing subAgentsCanceling=false")
		m.subAgentsCanceling = false
		return m, nil
	})
	regUpdatePlain(func(m Model, msg modeChangeMsg) (tea.Model, tea.Cmd) {
		m.mode = msg.Mode
		return m, nil
	})
	regUpdatePlain(func(m Model, msg cronPromptMsg) (tea.Model, tea.Cmd) { return m.handleCronPromptMsg(msg) })
	regUpdatePlain(func(m Model, msg skillsChangedMsg) (tea.Model, tea.Cmd) {
		m.refreshCommands()
		return m, nil
	})
	regUpdatePlain(func(m Model, msg updateCheckResultMsg) (tea.Model, tea.Cmd) {
		m.applyUpdateCheckResult(msg)
		return m, nil
	})
	regUpdatePlain(func(m Model, msg updateCheckTickMsg) (tea.Model, tea.Cmd) {
		return m, tea.Batch(m.checkForUpdateCmd(), m.scheduleUpdateCheckCmd())
	})
	regUpdatePlain(func(m Model, msg gitBranchTickMsg) (tea.Model, tea.Cmd) { return m.handleGitBranchTickMsg(msg) })
	regUpdatePlain(func(m Model, msg imPanelRefreshMsg) (tea.Model, tea.Cmd) { return m.handleImPanelRefreshMsg(msg) })
	regUpdatePlain(func(m Model, msg updatePrepareResultMsg) (tea.Model, tea.Cmd) { return m.handlePreparedUpdate(msg) })
	regUpdate(func(m Model, msg toolStatusMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleToolStatusMsg(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg agentToolBatchMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.noteTurnActivity()
		return m.handleAgentToolBatchMsg(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg agentToolStatusMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.noteTurnActivity()
		return m.handleAgentToolStatusMsg(msg, spinnerCmd)
	})
	regUpdatePlain(func(m Model, msg mcpServersMsg) (tea.Model, tea.Cmd) { return m.handleMcpServersMsg(msg) })
	regUpdatePlain(func(m Model, msg mcpInstallResultMsg) (tea.Model, tea.Cmd) { return m.handleMcpInstallResultMsg(msg) })
	regUpdatePlain(func(m Model, msg tunnelStartMsg) (tea.Model, tea.Cmd) { return m.handleTunnelStartMsg(msg) })
	regUpdatePlain(func(m Model, msg tunnelRefreshMsg) (tea.Model, tea.Cmd) { return m.handleTunnelRefreshMsg(msg) })
	regUpdatePlain(func(m Model, msg tunnelShareBootstrapMsg) (tea.Model, tea.Cmd) {
		return m.handleTunnelShareBootstrapMsg(msg)
	})
	regUpdatePlain(func(m Model, msg tunnelStopMsg) (tea.Model, tea.Cmd) {
		m.chatWriteSystem(nextSystemID(), m.t("tunnel.stopped"))
		m.chatListScrollToBottom()
		return m, nil
	})
	regUpdatePlain(func(m Model, msg tunnelInboundMsg) (tea.Model, tea.Cmd) { return m.handleTunnelInboundMsg(msg) })
	regUpdatePlain(func(m Model, msg tunnelClientConnectedMsg) (tea.Model, tea.Cmd) {
		return m.handleTunnelClientConnectedMsgForGeneration(msg.generation)
	})
	regUpdatePlain(func(m Model, msg tunnelModeChangeMsg) (tea.Model, tea.Cmd) { return m.handleTunnelModeChangeMsg(msg) })
	regUpdatePlain(func(m Model, msg tunnelApprovalResponseMsg) (tea.Model, tea.Cmd) {
		return m.handleTunnelApprovalResponse(msg)
	})
	regUpdatePlain(func(m Model, msg tunnelAskUserResponseMsg) (tea.Model, tea.Cmd) {
		return m.handleTunnelAskUserResponse(msg)
	})
	regUpdatePlain(func(m Model, msg tunnelLanguageChangeMsg) (tea.Model, tea.Cmd) {
		return m.handleTunnelLanguageChangeMsg(msg)
	})
	regUpdatePlain(func(m Model, msg tunnelThemeChangeMsg) (tea.Model, tea.Cmd) { return m.handleTunnelThemeChangeMsg(msg) })
	regUpdatePlain(func(m Model, msg wechatQRCodeMsg) (tea.Model, tea.Cmd) { return m.handleWechatQRCodeMsg(msg) })
	regUpdatePlain(func(m Model, msg wechatPollDueMsg) (tea.Model, tea.Cmd) { return m.handleWechatPollDueMsg(msg) })
	regUpdatePlain(func(m Model, msg wechatQRPollMsg) (tea.Model, tea.Cmd) { return m.handleWechatQRPollMsg(msg) })
	regUpdatePlain(func(m Model, msg qqBindResultMsg) (tea.Model, tea.Cmd) { return m.handleQqBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg imPanelResultMsg) (tea.Model, tea.Cmd) { return m.handleImPanelResultMsg(msg) })
	regUpdatePlain(func(m Model, msg feishuBindResultMsg) (tea.Model, tea.Cmd) { return m.handleFeishuBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg slackBindResultMsg) (tea.Model, tea.Cmd) { return m.handleSlackBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg discordBindResultMsg) (tea.Model, tea.Cmd) { return m.handleDiscordBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg whatsappBindResultMsg) (tea.Model, tea.Cmd) {
		return m.handleWhatsappBindResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg dingtalkBindResultMsg) (tea.Model, tea.Cmd) {
		return m.handleDingtalkBindResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg wecomBindResultMsg) (tea.Model, tea.Cmd) { return m.handleWecomBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg mattermostBindResultMsg) (tea.Model, tea.Cmd) {
		return m.handleMattermostBindResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg matrixBindResultMsg) (tea.Model, tea.Cmd) { return m.handleMatrixBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg signalBindResultMsg) (tea.Model, tea.Cmd) { return m.handleSignalBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg signalDaemonCheckMsg) (tea.Model, tea.Cmd) { return m.handleSignalDaemonCheckMsg(msg) })
	regUpdatePlain(func(m Model, msg signalQRCodeMsg) (tea.Model, tea.Cmd) { return m.handleSignalQRCodeMsg(msg) })
	regUpdatePlain(func(m Model, msg ircBindResultMsg) (tea.Model, tea.Cmd) { return m.handleIrcBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg nostrBindResultMsg) (tea.Model, tea.Cmd) { return m.handleNostrBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg twitchBindResultMsg) (tea.Model, tea.Cmd) { return m.handleTwitchBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg tgBindResultMsg) (tea.Model, tea.Cmd) { return m.handleTgBindResultMsg(msg) })
	regUpdatePlain(func(m Model, msg imEditResultMsg) (tea.Model, tea.Cmd) { return m.handleImEditResultMsg(msg) })
	regUpdatePlain(func(m Model, msg providerModelsRefreshResultMsg) (tea.Model, tea.Cmd) {
		return m.handleProviderModelsRefreshResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg providerAuthStartMsg) (tea.Model, tea.Cmd) { return m.handleProviderAuthStartMsg(msg) })
	regUpdatePlain(func(m Model, msg providerAuthResultMsg) (tea.Model, tea.Cmd) {
		return m.handleProviderAuthResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg modelPanelRefreshResultMsg) (tea.Model, tea.Cmd) {
		return m.handleModelPanelRefreshResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg mcpUninstallResultMsg) (tea.Model, tea.Cmd) {
		return m.handleMcpUninstallResultMsg(msg)
	})
	regUpdatePlain(func(m Model, msg mcpOAuthStartMsg) (tea.Model, tea.Cmd) { return m.handleMcpOAuthStartMsg(msg) })
	regUpdatePlain(func(m Model, msg mcpOAuthResultMsg) (tea.Model, tea.Cmd) { return m.handleMcpOAuthResultMsg(msg) })
	regUpdatePlain(func(m Model, msg mcpHealthCheckTickMsg) (tea.Model, tea.Cmd) { return m.handleMcpHealthCheckTick(msg) })
	regUpdatePlain(func(m Model, msg setProgramMsg) (tea.Model, tea.Cmd) { return m.handleSetProgramMsg(msg) })
	regUpdatePlain(func(m Model, msg inputDrainEndMsg) (tea.Model, tea.Cmd) { return m.handleInputDrainEndMsg(msg) })
	regUpdatePlain(func(m Model, msg imageAttachedMsg) (tea.Model, tea.Cmd) { return m.handleImageAttachedMsg(msg) })
	regUpdatePlain(func(m Model, msg textPasteMsg) (tea.Model, tea.Cmd) { return m.handleTextPasteMsg(msg) })
	regUpdate(func(m Model, msg statusMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleStatusMsg(msg, spinnerCmd)
	})
	regUpdate(func(m Model, msg agentStatusMsg, spinnerCmd tea.Cmd) (tea.Model, tea.Cmd) {
		return m.handleAgentStatusMsg(msg, spinnerCmd)
	})
	regNoop[agentRoundProgressMsg]()
	regUpdatePlain(func(m Model, msg agentRoundSummaryMsg) (tea.Model, tea.Cmd) { return m.handleAgentRoundSummaryMsg(msg) })
	// Don't emit IM here — AskUserMsg handler will emit the first question
	// after creating the questionnaire state.
	regNoop[agentAskUserMsg]()
	regUpdatePlain(func(m Model, msg verifyProgressMsg) (tea.Model, tea.Cmd) { return m.handleVerifyProgressMsg(msg) })
	regUpdatePlain(func(m Model, msg toolProgressMsg) (tea.Model, tea.Cmd) { return m.handleToolProgressMsg(msg) })
	regUpdatePlain(func(m Model, msg verifyResultMsg) (tea.Model, tea.Cmd) { return m.handleVerifyResultMsg(msg) })
	// LAN chat family (the original multi-label case): all seven message
	// types share handleLanChatPanelUpdate.
	regLanChatFamily()
}

// regLanChatFamily registers the seven LAN chat message types that the
// original switch handled in one multi-label case.
func regLanChatFamily() {
	regUpdatePlain(func(m Model, msg lanchatMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatReceiptMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatPeerJoinMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatPeerLeaveMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatApprovalReqMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatNickChangeMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
	regUpdatePlain(func(m Model, msg lanchatAutoApproveMsg) (tea.Model, tea.Cmd) { return m.handleLanChatPanelUpdate(msg) })
}

// dispatchUpdate looks up the exact dynamic type of msg in the table.
// Second return is false when no exact-type handler exists (the caller
// then runs the interface-typed residual switch / fall-through tail).
//
// Slow-handler instrumentation: the Bubble Tea loop is single-threaded, so
// any handler (or the View frame it triggers) blocking for seconds freezes
// input and the cursor — the user-reported startup freeze (2026-09-18)
// showed a 250ms input-drain tick landing 17s late with no attribution.
// Handlers over slowUpdateLogThreshold are logged with their message type
// so the next reproduction identifies the culprit directly from the debug
// log instead of requiring a profiler attach.
func (m Model) dispatchUpdate(msg tea.Msg, spinnerCmd tea.Cmd) (tea.Model, bool, tea.Cmd) {
	h, ok := updateHandlers[reflect.TypeOf(msg)]
	if !ok {
		return nil, false, nil
	}
	start := time.Now()
	model, cmd := h(m, msg, spinnerCmd)
	if d := time.Since(start); d > slowUpdateLogThreshold {
		debug.Log("tui", fmt.Sprintf("slow update handler: type=%T duration=%s", msg, d.Round(time.Millisecond)))
	}
	return model, true, cmd
}

// slowUpdateLogThreshold bounds normal handler cost with generous headroom:
// regular handlers run in microseconds; anything past 100ms is a startup
// stall worth attributing.
const slowUpdateLogThreshold = 100 * time.Millisecond
