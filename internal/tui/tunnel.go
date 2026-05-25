package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
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

// tunnelLanguageChangeMsg carries a language change from mobile.
type tunnelLanguageChangeMsg struct {
	language string
}

// tunnelThemeChangeMsg carries a theme change from mobile.
type tunnelThemeChangeMsg struct {
	theme string
}

type tunnelClientConnectedMsg struct{}

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
	msg.broker.OnRelayConnected(func(info tunnel.RelayConnectedState) {
		if info.Role == "client" && m.program != nil {
			m.program.Send(tunnelClientConnectedMsg{})
		}
	})

	msg.broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		return m.tunnelSnapshot()
	})
	msg.broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return m.currentSessionTunnelReplayEvents()
	})
	msg.broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		m.recordTunnelEvent(ev)
	})
	seededSnapshot, replayedCanonical := m.publishTunnelSnapshotForCurrentSessionWithReport(true)
	if !replayedCanonical {
		m.reseedTunnelSnapshotAfterStart(seededSnapshot)
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

func (m *Model) handleTunnelClientConnectedMsg() (tea.Model, tea.Cmd) {
	if m.tunnelSession == nil {
		return m, nil
	}
	if m.qrOverlay != nil {
		m.closeQROverlay()
	}
	m.chatWriteSystem(nextSystemID(), m.t("tunnel.mobile_connected"))
	m.chatListScrollToBottom()
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
		m.tunnelBroker.PushToolCall(ev.Tool.ID, name, title, string(ev.Tool.Arguments), present.Detail)
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()

	case provider.StreamEventToolResult:
		content := ev.Result
		if len([]rune(content)) > 2000 {
			content = truncateRunes(content, 2000, "\n...(truncated)")
		}
		m.pushTunnelToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventSystem:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()

	case provider.StreamEventDone:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()

	case provider.StreamEventError:
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		if ev.Error != nil {
			m.tunnelBroker.PushError(sanitizeAPIError(ev.Error).Error())
		}
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()
	}
}

// pushTunnelUserMessage echoes a locally-typed user message to the mobile client.
func (m *Model) pushTunnelUserMessage(text string) {
	if m.tunnelBroker != nil {
		if m.tunnelUserMessageOverride != nil {
			override := *m.tunnelUserMessageOverride
			if override.Text == "" {
				override.Text = text
			}
			m.tunnelUserMessageOverride = nil
			m.tunnelBroker.PushUserMessageData(override)
			return
		}
		m.tunnelBroker.PushUserMessage(text)
	}
}

func tunnelShellCommandData(prefix, text string) (tunnel.MessageData, bool) {
	text = strings.TrimSpace(text)
	prefix = strings.TrimSpace(prefix)
	if text == "" || (prefix != "$" && prefix != "!") {
		return tunnel.MessageData{}, false
	}
	return tunnel.MessageData{
		Text:        prefix + " " + text,
		DisplayText: text,
		Kind:        tunnel.MessageKindShellCommand,
	}, true
}

func (m *Model) setNextTunnelUserMessageOverride(data tunnel.MessageData) {
	m.tunnelUserMessageOverride = &data
}

func (m *Model) pushTunnelToolResult(toolID, toolName, result string, isError bool) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushToolResult(toolID, toolName, result, isError)
	}
}

// pushTunnelStatus sends a main-agent status update to the mobile client.
func (m *Model) pushTunnelStatus(status, message string) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushStatus(status, message)
	}
}

func (m *Model) pushTunnelActivity(activity string) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushActivity(strings.TrimSpace(activity))
	}
}

func (m *Model) pushTunnelCurrentStatus() {
	status := m.currentTunnelStatus()
	m.pushTunnelStatus(status.Status, status.Message)
}

func (m *Model) pushTunnelCurrentActivity() {
	m.pushTunnelActivity(m.currentTunnelActivity())
}

// pushTunnelCancel notifies mobile that the current run was cancelled.
func (m *Model) pushTunnelCancel() {
	if m.tunnelBroker != nil {
		m.tunnelBroker.PushTextDone(m.tunnelMsgID)
		m.pushTunnelStatus(tunnel.StatusIdle, "cancelled")
		m.pushTunnelActivity("")
		m.tunnelMsgID = m.tunnelBroker.NextMessageID()
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
		// Acknowledge to mobile client that the message was received by desktop.
		if m.tunnelBroker != nil {
			m.tunnelBroker.PushServerAck(data.MessageID)
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

	case tunnel.CmdLanguageChange:
		var data tunnel.LanguageChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if data.Language != "" && m.program != nil {
			m.program.Send(tunnelLanguageChangeMsg{language: data.Language})
		}

	case tunnel.CmdThemeChange:
		var data tunnel.ThemeChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if data.Theme != "" && m.program != nil {
			m.program.Send(tunnelThemeChangeMsg{theme: data.Theme})
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

// handleTunnelLanguageChangeMsg switches the UI language from a mobile request.
func (m *Model) handleTunnelLanguageChangeMsg(msg tunnelLanguageChangeMsg) (tea.Model, tea.Cmd) {
	lang := normalizeLanguage(msg.language)
	if lang == m.currentLanguage() {
		return m, nil
	}
	m.setLanguage(msg.language)
	// Notify mobile that language changed (echo back)
	if m.tunnelBroker != nil {
		m.tunnelBroker.SendLanguageChange(msg.language)
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Language changed to %s (from mobile)", lang))
	m.chatListScrollToBottom()
	return m, nil
}

// handleTunnelThemeChangeMsg handles a theme change from mobile.
// TUI does not switch its own theme, but echoes the event to other clients.
func (m *Model) handleTunnelThemeChangeMsg(msg tunnelThemeChangeMsg) (tea.Model, tea.Cmd) {
	if m.tunnelBroker != nil {
		m.tunnelBroker.SendThemeChange(msg.theme)
	}
	return m, nil
}

// ─── Helpers ───

// currentSessionMessages returns messages from the current agent session, if any.
func (m *Model) currentSessionMessages() []provider.Message {
	if m.agent != nil {
		if msgs := m.agent.Messages(); len(msgs) > 0 {
			return msgs
		}
	}
	if m.session != nil {
		return m.session.Messages
	}
	return nil
}

func (m *Model) currentTunnelHistory() []tunnel.HistoryEntry {
	if m.chatList == nil || m.chatList.Len() == 0 {
		return tunnelMessagesToHistory(m.currentSessionMessages())
	}

	history := make([]tunnel.HistoryEntry, 0, m.chatList.Len()*2)
	for i := 0; i < m.chatList.Len(); i++ {
		item := m.chatList.ItemAt(i)
		switch it := item.(type) {
		case *chat.UserItem:
			if text := strings.TrimSpace(it.Text()); text != "" {
				if data, ok := tunnelShellCommandData(it.Prefix(), text); ok {
					history = append(history, tunnel.HistoryEntry{
						Role:        "user",
						Content:     data.Text,
						DisplayText: data.DisplayText,
						Kind:        data.Kind,
					})
				} else {
					history = append(history, tunnel.HistoryEntry{Role: "user", Content: text})
				}
			}
		case *chat.AssistantItem:
			if text := strings.TrimSpace(it.Text()); text != "" {
				history = append(history, tunnel.HistoryEntry{Role: "assistant", Content: text})
			}
		case *chat.SystemItem:
			if text := strings.TrimSpace(it.Text()); text != "" {
				if _, ok := m.shellOutputIDs[it.ID()]; ok {
					history = append(history, tunnel.HistoryEntry{
						Role:    "assistant",
						Content: text,
						Kind:    tunnel.MessageKindShellOutput,
					})
				} else {
					history = append(history, tunnel.HistoryEntry{Role: "system", Content: text})
				}
			}
		case interface {
			ID() string
			ToolName() string
			Input() string
			Result() string
			IsError() bool
		}:
			rawArgs := it.Input()
			present := describeTool(m.currentLanguage(), it.ToolName(), rawArgs)
			argsStr := rawArgs
			if len(argsStr) > 200 {
				argsStr = argsStr[:200] + "..."
			}
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          it.ID(),
				ToolName:        it.ToolName(),
				ToolDisplayName: toolCallDisplayName(it.ToolName(), rawArgs),
				ToolArgs:        argsStr,
				ToolDetail:      present.Detail,
			})
			if result := strings.TrimSpace(it.Result()); result != "" {
				history = append(history, tunnel.HistoryEntry{
					Role:     "tool_result",
					ToolID:   it.ID(),
					ToolName: it.ToolName(),
					Result:   result,
					IsError:  it.IsError(),
				})
			}
		case *chat.AgentToolItem:
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          it.ID(),
				ToolName:        "spawn_agent",
				ToolDisplayName: it.Label(),
			})
			if result := strings.TrimSpace(it.Result()); result != "" {
				history = append(history, tunnel.HistoryEntry{
					Role:     "tool_result",
					ToolID:   it.ID(),
					ToolName: "spawn_agent",
					Result:   result,
					IsError:  it.Status() == chat.StatusError || it.Status() == chat.StatusCanceled,
				})
			}
		}
	}
	return history
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

func (m *Model) tunnelSnapshot() tunnel.BrokerSnapshot {
	history := m.currentTunnelHistory()
	if tail := m.currentIncompleteTunnelHistoryTail(); len(tail) > 0 {
		history = mergeTunnelHistory(history, tail)
	}
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{
			Workspace: m.sidebarWorkingDirectory(),
			Model:     m.activeModel,
			Provider:  m.activeVendor,
			Mode:      m.mode.String(),
			Version:   version.Version,
		},
		Status: m.currentTunnelStatus(),
		Activity: tunnel.ActivityData{
			Activity: m.currentTunnelActivity(),
		},
	}
	if len(history) > 0 {
		snapshot.History = history
	}
	if extra := m.currentTunnelAgentSnapshotEvents(); len(extra) > 0 {
		snapshot.ExtraEvents = extra
	}
	return snapshot
}

func (m *Model) currentTunnelAgentSnapshotEvents() []tunnel.SnapshotEvent {
	var out []tunnel.SnapshotEvent
	if m.subAgentMgr != nil {
		agents := m.subAgentMgr.List()
		sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
		for _, sa := range agents {
			out = append(out, tunnelSnapshotEventsFromSubagent(sa)...)
		}
	}
	if m.swarmMgr != nil {
		teams := m.swarmMgr.ListTeams()
		sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })
		for _, team := range teams {
			mates := append([]swarm.TeammateSnapshot(nil), team.Teammates...)
			sort.Slice(mates, func(i, j int) bool { return mates[i].ID < mates[j].ID })
			for _, tm := range mates {
				out = append(out, tunnelSnapshotEventsFromTeammate(tm, team.ID)...)
			}
		}
	}
	return out
}

func tunnelSnapshotEventsFromSubagent(sa *subagent.SubAgent) []tunnel.SnapshotEvent {
	if sa == nil || strings.TrimSpace(sa.ID) == "" {
		return nil
	}
	out := []tunnel.SnapshotEvent{snapshotEvent(
		tunnel.EventSubagentSpawn,
		sa.ID,
		tunnel.SubagentSpawnData{AgentID: sa.ID, Name: sa.Name, Task: sa.Task},
	)}
	out = append(out, tunnelSnapshotAgentEvents(sa.ID, "sa-"+sa.ID, "", sa.Events(), sa.Result, errorString(sa.Error), string(sa.Status), sa.CurrentTool, sa.Name)...)
	return out
}

func tunnelSnapshotEventsFromTeammate(tm swarm.TeammateSnapshot, teamID string) []tunnel.SnapshotEvent {
	if strings.TrimSpace(tm.ID) == "" {
		return nil
	}
	out := []tunnel.SnapshotEvent{snapshotEvent(
		tunnel.EventSubagentSpawn,
		tm.ID,
		tunnel.SubagentSpawnData{AgentID: tm.ID, Name: tm.Name, Task: "teammate", Color: tm.Color, ParentID: teamID},
	)}
	events := make([]subagent.AgentEvent, 0, len(tm.Events))
	for _, ev := range tm.Events {
		converted := subagent.AgentEvent{
			Text:     ev.Text,
			ToolName: ev.ToolName,
			ToolID:   ev.ToolID,
			ToolArgs: ev.ToolArgs,
			Result:   ev.Result,
			IsError:  ev.IsError,
		}
		switch ev.Type {
		case swarm.TeammateEventToolCall:
			converted.Type = subagent.AgentEventToolCall
		case swarm.TeammateEventToolResult:
			converted.Type = subagent.AgentEventToolResult
		case swarm.TeammateEventError:
			converted.Type = subagent.AgentEventError
		default:
			converted.Type = subagent.AgentEventText
		}
		events = append(events, converted)
	}
	status := "running"
	if tm.Status == swarm.TeammateIdle || tm.Status == swarm.TeammateShuttingDown {
		status = "completed"
	}
	out = append(out, tunnelSnapshotAgentEvents(tm.ID, "tm-"+tm.ID, tm.Color, events, tm.LastResult, "", status, tm.CurrentTask, tm.Name)...)
	return out
}

func tunnelSnapshotAgentEvents(agentID, textID, color string, events []subagent.AgentEvent, result, errText, status, statusMessage, name string) []tunnel.SnapshotEvent {
	var out []tunnel.SnapshotEvent
	textBuf := strings.Builder{}
	toolArgsByID := make(map[string]string)
	flushText := func(done bool) {
		if textBuf.Len() == 0 {
			return
		}
		out = append(out, snapshotEvent(
			tunnel.EventSubagentText,
			agentID,
			tunnel.SubagentTextData{AgentID: agentID, ID: textID, Chunk: textBuf.String(), Done: done},
		))
		textBuf.Reset()
	}
	for _, ev := range events {
		switch ev.Type {
		case subagent.AgentEventToolCall:
			flushText(false)
			if ev.ToolID != "" {
				toolArgsByID[ev.ToolID] = ev.ToolArgs
			}
			detail := describeTool(LangEnglish, ev.ToolName, ev.ToolArgs).Detail
			out = append(out, snapshotEvent(
				tunnel.EventSubagentToolCall,
				agentID,
				tunnel.SubagentToolCallData{
					AgentID:     agentID,
					ToolID:      ev.ToolID,
					ToolName:    ev.ToolName,
					DisplayName: toolCallDisplayName(ev.ToolName, ev.ToolArgs),
					Args:        ev.ToolArgs,
					Detail:      detail,
				},
			))
		case subagent.AgentEventToolResult:
			flushText(false)
			present, _ := toolpkg.DescribeToolResult(ev.ToolName, toolArgsByID[ev.ToolID], ev.Result, ev.IsError)
			delete(toolArgsByID, ev.ToolID)
			out = append(out, snapshotEvent(
				tunnel.EventSubagentToolResult,
				agentID,
				tunnel.SubagentToolResultData{
					AgentID:     agentID,
					ToolID:      ev.ToolID,
					ToolName:    ev.ToolName,
					Result:      ev.Result,
					Summary:     present.Summary,
					Payload:     present.Payload,
					PayloadMode: present.PayloadMode,
					IsError:     ev.IsError,
				},
			))
		case subagent.AgentEventError:
			if ev.Text != "" {
				if textBuf.Len() > 0 {
					textBuf.WriteString("\n")
				}
				textBuf.WriteString(ev.Text)
			}
		default:
			if ev.Text != "" {
				textBuf.WriteString(ev.Text)
			}
		}
	}
	if textBuf.Len() == 0 {
		switch {
		case result != "":
			textBuf.WriteString(result)
		case errText != "":
			textBuf.WriteString(errText)
		}
	}
	completed := status == "completed" || status == "failed" || status == "cancelled"
	flushText(completed)
	if completed {
		summary := result
		success := errText == ""
		if summary == "" {
			summary = errText
		}
		if summary == "" {
			summary = status
		}
		out = append(out, snapshotEvent(
			tunnel.EventSubagentComplete,
			agentID,
			tunnel.SubagentCompleteData{AgentID: agentID, Name: name, Summary: summary, Success: success},
		))
		return out
	}
	if statusMessage == "" {
		statusMessage = name
	}
	out = append(out, snapshotEvent(
		tunnel.EventSubagentStatus,
		agentID,
		tunnel.SubagentStatusData{AgentID: agentID, Status: tunnel.StatusRunning, Message: statusMessage},
	))
	return out
}

func snapshotEvent(eventType, streamID string, data interface{}) tunnel.SnapshotEvent {
	raw, _ := json.Marshal(data)
	return tunnel.SnapshotEvent{
		Type:     eventType,
		StreamID: streamID,
		Data:     raw,
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *Model) announceTunnelActiveSession() {
	if m.tunnelBroker == nil || m.session == nil || m.session.ID == "" {
		return
	}
	m.tunnelBroker.AnnounceActiveSession(m.session.ID)
}

func (m *Model) publishTunnelSnapshotForCurrentSession(reset bool) {
	_, _ = m.publishTunnelSnapshotForCurrentSessionWithReport(reset)
}

func (m *Model) publishTunnelSnapshotForCurrentSessionWithReport(reset bool) (tunnel.BrokerSnapshot, bool) {
	snapshot := m.tunnelSnapshot()
	if m.tunnelBroker == nil {
		return snapshot, false
	}
	switchedSession := false
	if m.session != nil && m.session.ID != "" {
		if reset {
			m.tunnelBroker.SwitchSession(m.session.ID)
			switchedSession = true
		} else {
			m.tunnelBroker.AnnounceActiveSession(m.session.ID)
		}
	}
	m.prepareCurrentSessionTunnelLedger()
	if events := m.currentSessionTunnelReplayEvents(); len(events) > 0 {
		m.tunnelBroker.ReplayEvents(events, reset && !switchedSession)
		return snapshot, true
	}
	m.tunnelBroker.SendSnapshot(snapshot)
	return snapshot, false
}

func tunnelSnapshotMatches(a, b tunnel.BrokerSnapshot) bool {
	if a.SessionInfo != b.SessionInfo || a.Status != b.Status || a.Activity != b.Activity {
		return false
	}
	return tunnelHistoryMatches(a.History, b.History) && tunnelSnapshotEventMatches(a.ExtraEvents, b.ExtraEvents)
}

func tunnelSnapshotEventMatches(a, b []tunnel.SnapshotEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].StreamID != b[i].StreamID || string(a[i].Data) != string(b[i].Data) {
			return false
		}
	}
	return true
}

func (m *Model) reseedTunnelSnapshotAfterStart(seeded tunnel.BrokerSnapshot) {
	if m.tunnelBroker == nil {
		return
	}
	latest := m.tunnelSnapshot()
	if tunnelSnapshotMatches(seeded, latest) {
		return
	}
	if m.session != nil && m.session.ID != "" {
		m.tunnelBroker.SwitchSession(m.session.ID)
	} else {
		m.tunnelBroker.ResetSession()
	}
	m.tunnelBroker.SendSnapshot(latest)
}

func (m *Model) currentSessionTunnelReplayEvents() []tunnel.GatewayMessage {
	if m.session == nil || !m.session.TunnelEventsComplete || len(m.session.TunnelEvents) == 0 {
		return nil
	}
	out := make([]tunnel.GatewayMessage, 0, len(m.session.TunnelEvents))
	for _, ev := range m.session.TunnelEvents {
		out = append(out, tunnel.GatewayMessage{
			SessionID: m.session.ID,
			EventID:   ev.EventID,
			StreamID:  ev.StreamID,
			Type:      ev.Type,
			Data:      ev.Data,
		})
	}
	return out
}

func tunnelEventsToHistory(events []session.TunnelEvent) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	textByID := make(map[string]string)
	textKindByID := make(map[string]string)
	finalizeText := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		text := strings.TrimSpace(textByID[id])
		delete(textByID, id)
		kind := strings.TrimSpace(textKindByID[id])
		delete(textKindByID, id)
		if text == "" {
			return
		}
		history = append(history, tunnel.HistoryEntry{
			Role:    "assistant",
			Content: text,
			Kind:    kind,
		})
	}

	for _, ev := range events {
		switch ev.Type {
		case tunnel.EventUserMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if data.Kind == tunnel.MessageKindCron {
				text := strings.TrimSpace(data.DisplayText)
				if text == "" {
					text = strings.TrimSpace(data.Text)
				}
				if text == "" {
					continue
				}
				history = append(history, tunnel.HistoryEntry{
					Role:    "system",
					Content: text,
				})
				continue
			}
			text := strings.TrimSpace(data.Text)
			if text == "" {
				text = strings.TrimSpace(data.DisplayText)
			}
			if text == "" {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:        "user",
				Content:     text,
				DisplayText: strings.TrimSpace(data.DisplayText),
				Kind:        data.Kind,
			})
		case tunnel.EventSystemMessage:
			var data tunnel.MessageData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			text := strings.TrimSpace(data.Text)
			if text == "" {
				text = strings.TrimSpace(data.DisplayText)
			}
			if text == "" {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:        "system",
				Content:     text,
				DisplayText: strings.TrimSpace(data.DisplayText),
				Kind:        data.Kind,
			})
		case tunnel.EventText:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if strings.TrimSpace(data.ID) == "" || data.Chunk == "" {
				continue
			}
			textByID[data.ID] += data.Chunk
			if strings.TrimSpace(data.Kind) != "" && strings.TrimSpace(textKindByID[data.ID]) == "" {
				textKindByID[data.ID] = strings.TrimSpace(data.Kind)
			}
			if data.Done {
				finalizeText(data.ID)
			}
		case tunnel.EventTextDone:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			id := data.ID
			if strings.TrimSpace(id) == "" {
				id = ev.StreamID
			}
			finalizeText(id)
		case tunnel.EventToolCall:
			var data tunnel.ToolCallData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          data.ToolID,
				ToolName:        data.ToolName,
				ToolDisplayName: data.DisplayName,
				ToolArgs:        data.Args,
				ToolDetail:      data.Detail,
			})
		case tunnel.EventToolResult:
			var data tunnel.ToolResultData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:     "tool_result",
				ToolID:   data.ToolID,
				ToolName: data.ToolName,
				Result:   data.Result,
				IsError:  data.IsError,
			})
		case tunnel.EventError:
			var data tunnel.ErrorData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			text := strings.TrimSpace(data.Message)
			if text == "" {
				continue
			}
			history = append(history, tunnel.HistoryEntry{
				Role:    "error",
				Content: text,
			})
		}
	}

	return history
}

func tunnelHistoryMatches(a, b []tunnel.HistoryEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Role != b[i].Role ||
			a[i].Content != b[i].Content ||
			a[i].ToolID != b[i].ToolID ||
			a[i].ToolName != b[i].ToolName ||
			a[i].ToolDisplayName != b[i].ToolDisplayName ||
			a[i].ToolArgs != b[i].ToolArgs ||
			a[i].ToolDetail != b[i].ToolDetail ||
			a[i].Result != b[i].Result ||
			a[i].IsError != b[i].IsError {
			return false
		}
	}
	return true
}

func (m *Model) currentIncompleteTunnelHistoryTail() []tunnel.HistoryEntry {
	m.sessionMutex().Lock()
	if m.session == nil || m.session.TunnelEventsComplete || len(m.session.TunnelEvents) == 0 {
		m.sessionMutex().Unlock()
		return nil
	}
	events := append([]session.TunnelEvent(nil), m.session.TunnelEvents...)
	m.sessionMutex().Unlock()
	return tunnelEventsToHistory(events)
}

func mergeTunnelHistory(base, tail []tunnel.HistoryEntry) []tunnel.HistoryEntry {
	if len(tail) == 0 {
		return base
	}
	if len(base) == 0 {
		return append([]tunnel.HistoryEntry(nil), tail...)
	}
	maxOverlap := len(base)
	if len(tail) < maxOverlap {
		maxOverlap = len(tail)
	}
	overlap := 0
	for size := maxOverlap; size > 0; size-- {
		if tunnelHistoryMatches(base[len(base)-size:], tail[:size]) {
			overlap = size
			break
		}
	}
	out := append([]tunnel.HistoryEntry(nil), base...)
	out = append(out, tail[overlap:]...)
	return out
}

func (m *Model) prepareCurrentSessionTunnelLedger() {
	snapshotHistory := m.currentTunnelHistory()

	m.sessionMutex().Lock()
	if m.session == nil || m.sessionStore == nil {
		m.sessionMutex().Unlock()
		return
	}
	needsSave := false
	switch {
	case m.session.TunnelEventsComplete:
		if tunnelHistoryMatches(tunnelEventsToHistory(m.session.TunnelEvents), snapshotHistory) {
			m.sessionMutex().Unlock()
			return
		}
		m.session.TunnelEvents = nil
		m.session.TunnelEventsComplete = false
		needsSave = true
	case len(snapshotHistory) == 0:
		m.session.TunnelEvents = nil
		m.session.TunnelEventsComplete = true
		needsSave = true
	case len(m.session.TunnelEvents) > 0:
		m.session.TunnelEvents = nil
		needsSave = true
	}
	if !needsSave {
		m.sessionMutex().Unlock()
		return
	}
	ses := m.session
	store := m.sessionStore
	m.sessionMutex().Unlock()

	_ = store.Save(ses)
}

func (m *Model) resetCurrentSessionTunnelLedger() {
	m.sessionMutex().Lock()
	if m.session == nil || m.sessionStore == nil {
		m.sessionMutex().Unlock()
		return
	}
	m.session.TunnelEvents = nil
	m.session.TunnelEventsComplete = false
	ses := m.session
	store := m.sessionStore
	m.sessionMutex().Unlock()

	_ = store.Save(ses)
}

func (m *Model) recordTunnelEvent(ev tunnel.GatewayMessage) {
	m.sessionMutex().Lock()
	if m.session == nil || m.sessionStore == nil || ev.EventID == "" || ev.Type == tunnel.EventSnapshotReset {
		m.sessionMutex().Unlock()
		return
	}
	record := session.TunnelEvent{
		EventID:  ev.EventID,
		StreamID: ev.StreamID,
		Type:     ev.Type,
		Data:     append([]byte(nil), ev.Data...),
	}
	m.session.TunnelEvents = append(m.session.TunnelEvents, record)
	ses := m.session
	store := m.sessionStore
	m.sessionMutex().Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendTunnelEventToDisk(ses, record)
	} else {
		m.sessionMutex().Lock()
		_ = store.Save(ses)
		m.sessionMutex().Unlock()
	}
}

func (m *Model) currentTunnelStatus() tunnel.StatusData {
	if m.loading {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

func (m *Model) currentTunnelActivity() string {
	return strings.TrimSpace(m.statusActivity)
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
	m.pushTunnelCurrentStatus()

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
	m.pushTunnelCurrentStatus()
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
	m.pushTunnelCurrentStatus()
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
