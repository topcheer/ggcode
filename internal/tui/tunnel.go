package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/version"
)

// tunnelStartMsg is sent when the tunnel is ready.
type tunnelStartMsg struct {
	info    *tunnel.SessionInfo
	session *tunnel.Session
	broker  *tunnel.Broker
	err     error
}

// tunnelStopMsg is sent when the tunnel has stopped.
type tunnelStopMsg struct{}

// tunnelInboundMsg carries a user message from the mobile client into the
// Bubble Tea event loop. It is produced by the broker OnCommand callback
// (which runs on a goroutine) and consumed by Update.
type tunnelInboundMsg struct {
	text string
}

// tunnelModeChangeMsg carries a mode change request from mobile.
type tunnelModeChangeMsg struct {
	mode string
}

// tunnelApprovalResponseMsg carries an approval decision from mobile.
type tunnelApprovalResponseMsg struct {
	id       string
	decision string // "allow", "deny", "always"
}

// tunnelAskUserResponseMsg carries an ask_user answer from mobile.
type tunnelAskUserResponseMsg struct {
	id      string
	status  string
	answers []tunnel.AskUserAnswer
}

// ─── Slash command handler ───

func (m *Model) handleTunnelCommand(text string) tea.Cmd {
	// Accept both /tunnel and /share as prefix
	switch {
	case strings.HasPrefix(text, "/share"):
		text = strings.TrimPrefix(text, "/share")
	case strings.HasPrefix(text, "/tunnel"):
		text = strings.TrimPrefix(text, "/tunnel")
	}
	args := strings.TrimSpace(text)

	switch args {
	case "stop", "close", "off":
		if m.tunnelSession != nil {
			m.closeTunnelGracefully(2 * time.Second)
			m.chatWriteSystem(nextSystemID(), "Tunnel closed.")
		} else {
			m.chatWriteSystem(nextSystemID(), "No active tunnel.")
		}
		return nil

	case "status":
		if m.tunnelSession == nil {
			m.chatWriteSystem(nextSystemID(), "No active tunnel. Use /tunnel to start one.")
		} else {
			info := m.tunnelSession.Info()
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Relay active:\n  Connect: %s", info.ConnectURL))
		}
		return nil

	case "", "start", "on":
		if m.tunnelSession != nil {
			// Already active — re-show QR overlay
			info := m.tunnelSession.Info()
			m.openQROverlayDirect(
				"Mobile Tunnel",
				"Scan with GGCode Mobile to connect",
				info.QRCode,
				info.ConnectURL,
			)
			return nil
		}
		m.chatWriteSystem(nextSystemID(), "Starting tunnel...")
		return m.startTunnel()

	default:
		m.chatWriteSystem(nextSystemID(), "Usage: /tunnel [start|stop|status]")
		return nil
	}
}

func (m *Model) closeTunnelGracefully(timeout time.Duration) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.StopSharingGracefully(timeout)
	} else if m.tunnelSession != nil {
		m.tunnelSession.StopGracefully(timeout)
	}
	m.tunnelSession = nil
	m.tunnelBroker = nil
	m.tunnelMsgID = ""
	m.tunnelPendingApprovalID = ""
	m.tunnelPendingAskUserID = ""
	m.tunnelSpawned = nil
}

// ─── Tunnel lifecycle ───

func (m *Model) startTunnel() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sess := tunnel.NewSession(tunnel.DefaultRelayURL)
		info, err := sess.Start(ctx)
		if err != nil {
			return tunnelStartMsg{err: err}
		}

		broker := tunnel.NewBroker(sess)
		return tunnelStartMsg{info: info, session: sess, broker: broker}
	}
}

func (m *Model) handleTunnelStartMsg(msg tunnelStartMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Tunnel failed: %v", msg.err))
		m.chatListScrollToBottom()
		return m, nil
	}

	m.tunnelSession = msg.session
	m.tunnelBroker = msg.broker
	m.tunnelMsgID = msg.broker.NextMessageID()
	m.tunnelSpawned = make(map[string]bool)

	// Register inbound command handler.
	msg.broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		m.handleTunnelClientCommand(cmd)
	})

	// Send initial session info.
	msg.broker.SendSessionInfo(tunnel.SessionInfoData{
		Workspace: m.sidebarWorkingDirectory(),
		Model:     m.activeModel,
		Provider:  m.activeVendor,
		Mode:      m.mode.String(),
		Version:   version.Version,
	})

	// Seed history from current session messages.
	if msgs := m.currentSessionMessages(); len(msgs) > 0 {
		history := tunnelMessagesToHistory(msgs)
		if len(history) > 0 {
			msg.broker.SeedHistory(history)
		}
	}

	// Open QR overlay with connect URL and QR code.
	m.openQROverlayDirect(
		"Mobile Tunnel",
		"Scan with GGCode Mobile to connect",
		msg.info.QRCode,
		msg.info.ConnectURL,
	)

	return m, nil
}

// ─── Outbound: Agent stream events → mobile ───

// pushTunnelEvent pushes a provider stream event to the mobile client.
// Called from the agent stream callback in submit.go. Nil-safe.
func (m *Model) pushTunnelEvent(ev provider.StreamEvent) {
	if m.tunnelBroker == nil {
		return
	}

	switch ev.Type {
	case provider.StreamEventText:
		m.tunnelBroker.PushText(m.tunnelMsgID, ev.Text)

	case provider.StreamEventToolCallDone:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		name := ev.Tool.Name
		if name == "" {
			name = "tool"
		}
		present := describeTool(m.currentLanguage(), name, string(ev.Tool.Arguments))
		title := toolCallDisplayName(name, string(ev.Tool.Arguments))
		m.tunnelBroker.PushStatus(tunnel.StatusRunning, name)
		m.tunnelBroker.PushToolCall(ev.Tool.ID, name, title, string(ev.Tool.Arguments), present.Detail)

	case provider.StreamEventToolResult:
		content := ev.Result
		if len([]rune(content)) > 2000 {
			content = truncateRunes(content, 2000, "\n...(truncated)")
		}
		m.tunnelBroker.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventSystem:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)

	case provider.StreamEventDone:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.tunnelBroker.PushStatus(tunnel.StatusIdle, "")
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()

	case provider.StreamEventError:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.tunnelBroker.PushStatus(tunnel.StatusError, "error")
	}
}

// pushTunnelUserMessage echoes a locally-typed user message to the mobile client.
func (m *Model) pushTunnelUserMessage(text string) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushUserMessage(text)
	}
}

// pushTunnelStatusThinking sends a thinking status to the mobile client.
func (m *Model) pushTunnelStatusThinking() {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushStatus(tunnel.StatusThinking, "processing")
	}
}

// pushTunnelCancel notifies mobile that the current run was cancelled.
func (m *Model) pushTunnelCancel() {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.tunnelBroker.PushStatus(tunnel.StatusIdle, "cancelled")
	}
}

// ─── Outbound: Sub-agent events → mobile ───

// pushSubAgentTunnelEvent pushes sub-agent lifecycle events to the mobile client.
func (m *Model) pushSubAgentTunnelEvent(sa *subagent.SubAgent) {
	if m.tunnelBroker == nil {
		return
	}

	switch sa.Status {
	case subagent.StatusRunning:
		if !m.tunnelSpawned[sa.ID] {
			m.tunnelSpawned[sa.ID] = true
			m.tunnelBroker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
		}
		m.tunnelBroker.PushSubagentStatus(sa.ID, tunnel.StatusRunning, sa.CurrentTool)

	case subagent.StatusCompleted:
		if sa.Result != "" {
			msgID := fmt.Sprintf("sa-%s", sa.ID)
			m.tunnelBroker.PushSubagentText(sa.ID, msgID, sa.Result, true)
		}
		m.tunnelBroker.PushSubagentComplete(sa.ID, sa.Name, sa.Result, true)

	case subagent.StatusFailed:
		errMsg := ""
		if sa.Error != nil {
			errMsg = sa.Error.Error()
		}
		m.tunnelBroker.PushSubagentComplete(sa.ID, sa.Name, errMsg, false)

	case subagent.StatusCancelled:
		m.tunnelBroker.PushSubagentComplete(sa.ID, sa.Name, "cancelled", false)
	}
}

// pushSubAgentTunnelStreamText pushes streaming text from a sub-agent.
func (m *Model) pushSubAgentTunnelStreamText(agentID, text string) {
	if m.tunnelBroker != nil {
		msgID := fmt.Sprintf("sa-%s", agentID)
		m.tunnelBroker.PushSubagentText(agentID, msgID, text, false)
	}
}

// pushSubAgentTunnelToolCall pushes a tool call from a sub-agent.
func (m *Model) pushSubAgentTunnelToolCall(agentID, toolID, toolName, displayName, args, detail string) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushSubagentToolCall(agentID, toolID, toolName, displayName, args, detail)
	}
}

// pushSubAgentTunnelToolResult pushes a tool result from a sub-agent.
func (m *Model) pushSubAgentTunnelToolResult(agentID, toolID, toolName, result string, isError bool) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushSubagentToolResult(agentID, toolID, toolName, result, isError)
	}
}

// ─── Outbound: Swarm events → mobile ───

// pushSwarmTunnelEvent pushes swarm/teammate events to the mobile client.
func (m *Model) pushSwarmTunnelEvent(ev swarm.Event) {
	if m.tunnelBroker == nil {
		return
	}

	switch ev.Type {
	case "teammate_tool_call":
		detail := describeTool(LangEnglish, ev.CurrentTool, ev.ToolArgs).Detail
		title := toolCallDisplayName(ev.CurrentTool, ev.ToolArgs)
		m.tunnelBroker.PushSubagentToolCall(ev.TeammateID, ev.ToolID, ev.CurrentTool, title, ev.ToolArgs, detail)
		m.tunnelBroker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.CurrentTool)

	case "teammate_tool_result":
		m.tunnelBroker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, ev.ToolArgs, ev.IsError)

	case "teammate_text":
		msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
		m.tunnelBroker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)

	case "teammate_spawned":
		color := ""
		if m.swarmMgr != nil {
			if snap, ok := m.swarmMgr.TeammateSnapshot(ev.TeammateID); ok {
				color = snap.Color
			}
		}
		m.tunnelBroker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", color, ev.TeamID)

	case "teammate_working":
		m.tunnelBroker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.TeammateName)
		if m.swarmMgr != nil {
			if snap, ok := m.swarmMgr.TeammateSnapshot(ev.TeammateID); ok && len(snap.Events) > 0 {
				last := snap.Events[len(snap.Events)-1]
				if last.Type == swarm.TeammateEventText && last.Text != "" {
					msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
					m.tunnelBroker.PushSubagentText(ev.TeammateID, msgID, last.Text, false)
				}
			}
		}

	case "teammate_idle":
		if ev.Result != "" {
			msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
			m.tunnelBroker.PushSubagentText(ev.TeammateID, msgID, ev.Result, true)
		}
		success := ev.Error == nil
		summary := ev.Result
		if ev.Error != nil {
			summary = ev.Error.Error()
		}
		m.tunnelBroker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, summary, success)

	case "teammate_shutdown":
		m.tunnelBroker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, "shutdown", true)
	}
}

// ─── Inbound: Mobile → agent ───

// handleTunnelClientCommand is called from the broker's OnCommand callback
// (runs on a goroutine). It routes mobile commands into the Bubble Tea event loop.
func (m *Model) handleTunnelClientCommand(cmd tunnel.GatewayMessage) {
	switch cmd.Type {
	case tunnel.CmdMessage, "user_text":
		var data tunnel.MessageData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if data.Text == "" {
			return
		}
		if m.program != nil {
			m.program.Send(tunnelInboundMsg{text: data.Text})
		}

	case tunnel.CmdInterrupt:
		if m.program != nil {
			m.program.Send(tunnelInboundMsg{text: "/interrupt"})
		}

	case tunnel.CmdModeChange:
		var data tunnel.ModeChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if m.program != nil {
			m.program.Send(tunnelModeChangeMsg{mode: data.Mode})
		}

	case tunnel.CmdApprovalResponse:
		var data tunnel.ApprovalResponseData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if m.program != nil {
			m.program.Send(tunnelApprovalResponseMsg{id: data.ID, decision: data.Decision})
		}

	case tunnel.CmdAskUserResponse:
		var data tunnel.AskUserResponseData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if m.program != nil {
			m.program.Send(tunnelAskUserResponseMsg{id: data.ID, status: data.Status, answers: data.Answers})
		}
	}
}

// handleTunnelInboundMsg processes a user message from the mobile client.
// It routes through the same idle→startAgent / busy→queuePendingSubmission
// path as webchat messages.
func (m *Model) handleTunnelInboundMsg(msg tunnelInboundMsg) (tea.Model, tea.Cmd) {
	text := msg.text
	if text == "" {
		return m, nil
	}

	// Handle interrupt specially.
	if text == "/interrupt" {
		m.cancelActiveRun()
		return m, nil
	}

	// Notify Knight idle timer.
	if m.knight != nil {
		m.knight.NotifyActivity()
	}

	if m.cancelFunc == nil {
		// Agent idle — render user bubble and persist, then start agent.
		m.chatWriteUser(nextChatID(), text)
		m.chatListScrollToBottom()
		m.appendUserMessage(text)
		m.streamBuffer = nil
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
	// Agent busy — persist to session, queue for submission.
	// queuePendingSubmission will render the user bubble.
	m.appendUserMessage(text)
	m.queuePendingSubmission(text)
	return m, nil
}

// handleTunnelModeChangeMsg switches the permission mode from a mobile request.
func (m *Model) handleTunnelModeChangeMsg(msg tunnelModeChangeMsg) (tea.Model, tea.Cmd) {
	newMode := permission.ParsePermissionMode(msg.mode)
	if newMode == permission.SupervisedMode && msg.mode != "supervised" && msg.mode != "" {
		// ParsePermissionMode defaults to supervised for unknown values — reject.
		return m, nil
	}
	m.mode = newMode
	if cp, ok := m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(newMode)
	}
	m.persistModePreference()
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Mode changed to %s (from mobile)", newMode))
	m.chatListScrollToBottom()
	return m, nil
}

// ─── Helpers ───

// currentSessionMessages returns messages from the current agent session, if any.
func (m *Model) currentSessionMessages() []provider.Message {
	if m.agent == nil {
		return nil
	}
	return m.agent.Messages()
}

// tunnelMessagesToHistory converts provider messages to tunnel history entries.
func tunnelMessagesToHistory(msgs []provider.Message) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			var textParts []string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, strings.TrimSpace(block.Text))
					}
				case "tool_result":
					result := block.Output
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   result,
						IsError:  block.IsError,
					})
				}
			}
			if len(textParts) > 0 {
				history = append(history, tunnel.HistoryEntry{
					Role:    "user",
					Content: strings.Join(textParts, "\n"),
				})
			}
		case "assistant":
			for _, block := range msg.Content {
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "assistant",
						Content: strings.TrimSpace(block.Text),
					})
				} else if block.Type == "tool_use" {
					argsStr := string(block.Input)
					if len(argsStr) > 200 {
						argsStr = argsStr[:200] + "..."
					}
					present := describeTool(LangEnglish, block.ToolName, string(block.Input))
					history = append(history, tunnel.HistoryEntry{
						Role:            "tool_call",
						ToolID:          block.ToolID,
						ToolName:        block.ToolName,
						ToolDisplayName: toolCallDisplayName(block.ToolName, string(block.Input)),
						ToolArgs:        argsStr,
						ToolDetail:      present.Detail,
					})
				}
			}
		case "tool":
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					result := block.Output
					if len(result) > 500 {
						result = result[:500] + "..."
					}
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   result,
						IsError:  block.IsError,
					})
				}
			}
		}
	}
	return history
}

// truncateRunes truncates a string to maxRunes runes, appending suffix if truncated.
func truncateRunes(s string, maxRunes int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + suffix
}

func toolCallDisplayName(toolName, rawArgs string) string {
	args := parseToolArgs(rawArgs)
	if desc := argString(args, "description"); desc != "" {
		return desc
	}
	return prettifyToolName(toolName)
}

// parseModeFromString parses a permission mode string, returning (mode, true) if valid.
func parseModeFromString(s string) (permission.PermissionMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "supervised":
		return permission.SupervisedMode, true
	case "plan":
		return permission.PlanMode, true
	case "auto":
		return permission.AutoMode, true
	case "bypass":
		return permission.BypassMode, true
	case "autopilot":
		return permission.AutopilotMode, true
	default:
		return permission.SupervisedMode, false
	}
}

// ─── Inbound: Approval & Ask User response handlers ───

// handleTunnelApprovalResponse processes an approval decision from mobile.
func (m *Model) handleTunnelApprovalResponse(msg tunnelApprovalResponseMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval == nil {
		return m, nil
	}
	if m.tunnelPendingApprovalID != "" && msg.id != "" && msg.id != m.tunnelPendingApprovalID {
		return m, nil
	}

	var decision permission.Decision
	var cmd tea.Cmd

	switch msg.decision {
	case "allow":
		decision = permission.Allow
		cmd = m.handleApproval(decision)
	case "always_allow", "always":
		cmd = m.handleApprovalAllowAlways()
	default: // "deny" or unknown
		decision = permission.Deny
		cmd = m.handleApproval(decision)
	}

	return m, cmd
}

// handleTunnelAskUserResponse processes ask_user answers from mobile.
func (m *Model) handleTunnelAskUserResponse(msg tunnelAskUserResponseMsg) (tea.Model, tea.Cmd) {
	if m.pendingQuestionnaire == nil {
		return m, nil
	}
	if m.tunnelPendingAskUserID != "" && msg.id != "" && msg.id != m.tunnelPendingAskUserID {
		return m, nil
	}

	result := buildAskUserResponseFromTunnel(m.pendingQuestionnaire.request, msg.status, msg.answers)

	safego.Go("tunnel.askUserResponse", func() {
		select {
		case m.pendingQuestionnaire.response <- result:
		default:
		}
	})
	m.pendingQuestionnaire = nil
	m.tunnelPendingAskUserID = ""

	// Send status update to mobile
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushStatus(tunnel.StatusRunning, "")
	}

	return m, nil
}

func (m *Model) nextTunnelRequestID() string {
	if m.tunnelBroker == nil {
		return ""
	}
	return m.tunnelBroker.NextMessageID()
}

func (m *Model) pushTunnelApprovalResult(id, decision string) {
	if m.tunnelBroker == nil || strings.TrimSpace(id) == "" {
		return
	}
	m.tunnelBroker.PushApprovalResult(id, decision)
	m.tunnelBroker.PushStatus(tunnel.StatusRunning, "")
}

func (m *Model) pushTunnelAskUserResponse(id string, response toolpkg.AskUserResponse) {
	if m.tunnelBroker == nil || strings.TrimSpace(id) == "" {
		return
	}
	answers := make([]tunnel.AskUserAnswer, len(response.Answers))
	for i, answer := range response.Answers {
		answers[i] = tunnel.AskUserAnswer{
			QuestionID:   answer.ID,
			ChoiceIDs:    append([]string(nil), answer.SelectedChoiceIDs...),
			FreeformText: answer.FreeformText,
		}
	}
	m.tunnelBroker.PushAskUserResponse(id, response.Status, answers)
	m.tunnelBroker.PushStatus(tunnel.StatusRunning, "")
}

func tunnelDecisionString(decision permission.Decision) string {
	switch decision {
	case permission.Allow:
		return tunnel.DecisionAllow
	case permission.Deny:
		return tunnel.DecisionDeny
	default:
		return decision.String()
	}
}

func buildAskUserResponseFromTunnel(req toolpkg.AskUserRequest, status string, answers []tunnel.AskUserAnswer) toolpkg.AskUserResponse {
	normalizedStatus := strings.TrimSpace(status)
	if normalizedStatus == "" {
		normalizedStatus = toolpkg.AskUserStatusSubmitted
	}
	answerByQuestion := make(map[string]tunnel.AskUserAnswer, len(answers))
	for _, answer := range answers {
		answerByQuestion[answer.QuestionID] = answer
	}
	out := toolpkg.AskUserResponse{
		Status:        normalizedStatus,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		Answers:       make([]toolpkg.AskUserAnswer, 0, len(req.Questions)),
	}
	for _, question := range req.Questions {
		raw := answerByQuestion[question.ID]
		answer := buildAskUserAnswerFromSelection(question, raw.ChoiceIDs, raw.FreeformText)
		if answer.Answered {
			out.AnsweredCount++
		}
		out.Answers = append(out.Answers, answer)
	}
	return out
}

func buildAskUserAnswerFromSelection(question toolpkg.AskUserQuestion, selectedIDs []string, freeform string) toolpkg.AskUserAnswer {
	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = struct{}{}
	}
	orderedIDs := make([]string, 0, len(selectedSet))
	orderedLabels := make([]string, 0, len(selectedSet))
	for _, choice := range question.Choices {
		if _, ok := selectedSet[choice.ID]; ok {
			orderedIDs = append(orderedIDs, choice.ID)
			orderedLabels = append(orderedLabels, choice.Label)
		}
	}
	freeform = strings.TrimSpace(freeform)
	answerMode := toolpkg.AskUserAnswerModeNone
	completionStatus := toolpkg.AskUserCompletionUnanswered
	switch {
	case len(orderedIDs) == 0 && freeform == "":
		answerMode = toolpkg.AskUserAnswerModeNone
		completionStatus = toolpkg.AskUserCompletionUnanswered
	case len(orderedIDs) == 0 && freeform != "":
		answerMode = toolpkg.AskUserAnswerModeFreeformOnly
		if question.Kind == toolpkg.AskUserKindText {
			completionStatus = toolpkg.AskUserCompletionAnswered
		} else {
			completionStatus = toolpkg.AskUserCompletionPartial
		}
	case len(orderedIDs) > 0 && freeform == "":
		answerMode = toolpkg.AskUserAnswerModeSelectionOnly
		completionStatus = toolpkg.AskUserCompletionAnswered
	default:
		answerMode = toolpkg.AskUserAnswerModeSelectionAndFreeform
		completionStatus = toolpkg.AskUserCompletionAnswered
	}
	return toolpkg.AskUserAnswer{
		ID:                question.ID,
		Title:             question.Title,
		Kind:              question.Kind,
		CompletionStatus:  completionStatus,
		AnswerMode:        answerMode,
		Answered:          completionStatus == toolpkg.AskUserCompletionAnswered,
		SelectedChoiceIDs: orderedIDs,
		SelectedChoices:   orderedLabels,
		FreeformText:      freeform,
	}
}
