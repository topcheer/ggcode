package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/atotto/clipboard"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/debug"
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
	generation uint64
	info       *tunnel.SessionInfo
	session    *tunnel.Session
	broker     *tunnel.Broker
	err        error
}

type tunnelRefreshMsg struct {
	generation uint64
	session    *tunnel.Session
	info       *tunnel.SessionInfo
	err        error
}

type tunnelShareBootstrapMsg struct {
	generation uint64
}

// tunnelStopMsg is sent when the tunnel has stopped.
type tunnelStopMsg struct{}

// tunnelInboundMsg carries a user message from the mobile client into the
// Bubble Tea event loop. It is produced by the broker OnCommand callback
// (which runs on a goroutine) and consumed by Update.
type tunnelInboundMsg struct {
	generation uint64
	text       string
	messageID  string
}

// tunnelModeChangeMsg carries a mode change request from mobile.
type tunnelModeChangeMsg struct {
	generation uint64
	mode       string
}

// tunnelApprovalResponseMsg carries an approval decision from mobile.
type tunnelApprovalResponseMsg struct {
	generation uint64
	id         string
	decision   string // "allow", "deny", "always"
}

// tunnelAskUserResponseMsg carries an ask_user answer from mobile.
type tunnelAskUserResponseMsg struct {
	generation uint64
	id         string
	status     string
	answers    []tunnel.AskUserAnswer
}

// tunnelLanguageChangeMsg carries a language change from mobile.
type tunnelLanguageChangeMsg struct {
	generation uint64
	language   string
}

// tunnelThemeChangeMsg carries a theme change from mobile.
type tunnelThemeChangeMsg struct {
	generation uint64
	theme      string
}

type tunnelClientConnectedMsg struct {
	generation uint64
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
		if m.tunnelSession != nil || m.tunnelStarting {
			m.closeTunnelGracefullyAsync(2 * time.Second)
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
			status := fmt.Sprintf("Relay active:\n  Connect: %s", info.ConnectURL)
			if info.CompatibilityNotice != "" {
				status += "\n  Notice: " + info.CompatibilityNotice
			}
			m.chatWriteSystem(nextSystemID(), status)
		}
		return nil

	case "", "start", "on":
		// Always stop any existing share first — each share must create
		// a brand-new room so mobile clients never reconnect to a stale room.
		if m.tunnelSession != nil {
			m.closeTunnelGracefullyAsync(5 * time.Second)
		}
		if m.tunnelStarting {
			return nil
		}
		m.tunnelStarting = true
		generation := m.nextTunnelGeneration()
		m.chatWriteSystem(nextSystemID(), "Starting tunnel...")
		return m.startTunnel(generation)

	default:
		m.chatWriteSystem(nextSystemID(), "Usage: /tunnel [start|stop|status]")
		return nil
	}
}

func (m *Model) nextTunnelGeneration() uint64 {
	m.cancelTunnelShareBootstrapCapture()
	m.tunnelGeneration++
	return m.tunnelGeneration
}

func (m *Model) isCurrentTunnelGeneration(generation uint64) bool {
	return generation == 0 || generation == m.tunnelGeneration
}

func (m *Model) detachTunnelLifecycle() (*tunnel.Session, *tunnel.Broker) {
	sess := m.tunnelSession
	broker := m.tunnelBroker
	m.cancelTunnelShareBootstrapCapture()
	if broker != nil {
		broker.OnCommand(nil)
		broker.OnRelayConnected(nil)
		broker.SetSnapshotProvider(nil)
		broker.SetReplayProvider(nil)
		broker.SetEventRecorder(nil)
	}
	if sess != nil {
		sess.OnMessage(nil)
		sess.OnConnected(nil)
	}
	m.closeQROverlay()
	m.tunnelSession = nil
	m.tunnelBroker = nil

	// Detach online broker from unified TunnelHost
	if m.tunnelHost != nil {
		m.tunnelHost.DetachOnlineBroker()
	}
	m.resetTunnelMainStream()
	m.tunnelPendingApprovalID = ""
	m.tunnelPendingAskUserID = ""
	m.tunnelClientNoticeShown = false
	m.tunnelSpawned = nil
	m.tunnelStarting = false
	return sess, broker
}

func (m *Model) ensureTunnelShareBootstrapState() *tunnelShareBootstrapState {
	if m.tunnelShareBootstrap == nil {
		m.tunnelShareBootstrap = &tunnelShareBootstrapState{}
	}
	return m.tunnelShareBootstrap
}

func (m *Model) beginTunnelShareBootstrapCapture(generation uint64) {
	state := m.ensureTunnelShareBootstrapState()
	state.mu.Lock()
	state.generation = generation
	state.active = true
	state.pending = nil
	state.mu.Unlock()
}

func (m *Model) finishTunnelShareBootstrapCapture(generation uint64) []tunnel.GatewayMessage {
	state := m.ensureTunnelShareBootstrapState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.active || state.generation != generation {
		return nil
	}
	pending := append([]tunnel.GatewayMessage(nil), state.pending...)
	state.active = false
	state.pending = nil
	return pending
}

func (m *Model) cancelTunnelShareBootstrapCapture() {
	state := m.ensureTunnelShareBootstrapState()
	state.mu.Lock()
	state.active = false
	state.pending = nil
	state.mu.Unlock()
}

func (m *Model) captureTunnelShareBootstrapEvent(ev tunnel.GatewayMessage) bool {
	state := m.ensureTunnelShareBootstrapState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.active {
		return false
	}
	state.pending = append(state.pending, ev)
	return true
}

func stopDetachedTunnelGracefully(sess *tunnel.Session, broker *tunnel.Broker, timeout time.Duration) {
	agentruntime.StopSharedTunnelGracefully(sess, broker, timeout)
}

func (m *Model) closeTunnelGracefully(timeout time.Duration) {
	m.nextTunnelGeneration()
	sess, broker := m.detachTunnelLifecycle()
	stopDetachedTunnelGracefully(sess, broker, timeout)
}

func (m *Model) closeTunnelGracefullyAsync(timeout time.Duration) {
	m.nextTunnelGeneration()
	sess, broker := m.detachTunnelLifecycle()
	safego.Go("tui.tunnel.closeTunnelGracefully", func() {
		stopDetachedTunnelGracefully(sess, broker, timeout)
	})
}

// ─── Tunnel lifecycle ───

func (m *Model) startTunnel(generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sess := tunnel.NewSession(tunnel.DefaultRelayURL, tunnel.WithClientMetadata("tui", version.Version))
		info, err := sess.Start(ctx)
		if err != nil {
			return tunnelStartMsg{generation: generation, err: err}
		}

		broker := tunnel.NewBroker(sess)
		return tunnelStartMsg{generation: generation, info: info, session: sess, broker: broker}
	}
}

func (m *Model) refreshTunnelInvite(generation uint64, sess *tunnel.Session) tea.Cmd {
	return func() tea.Msg {
		if sess == nil {
			return tunnelRefreshMsg{generation: generation, err: fmt.Errorf("tunnel session: no active session")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := sess.RefreshInvite(ctx)
		return tunnelRefreshMsg{generation: generation, session: sess, info: info, err: err}
	}
}

func (m *Model) handleTunnelStartMsg(msg tunnelStartMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentTunnelGeneration(msg.generation) {
		if msg.broker != nil || msg.session != nil {
			safego.Go("tui.tunnel.discardStaleStart", func() {
				agentruntime.StopSharedTunnelGracefully(msg.session, msg.broker, 2*time.Second)
			})
		}
		return m, nil
	}
	m.tunnelStarting = false
	if msg.err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Tunnel failed: %v", msg.err))
		m.chatListScrollToBottom()
		return m, nil
	}

	m.bindTunnelProjectionSession()
	m.tunnelSession = msg.session
	m.tunnelBroker = msg.broker

	// Attach online broker to unified TunnelHost
	if m.tunnelHost != nil {
		m.tunnelHost.AttachOnlineBroker(msg.broker)
	}
	m.tunnelSpawned = make(map[string]bool)

	// Register inbound command handler.
	currentBroker := msg.broker
	currentGeneration := msg.generation
	msg.broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		m.handleTunnelClientCommand(currentGeneration, currentBroker, cmd)
	})
	msg.broker.OnRelayConnected(func(info tunnel.RelayConnectedState) {
		if info.Role == "client" && m.program != nil {
			m.program.Send(tunnelClientConnectedMsg{generation: currentGeneration})
		}
	})

	msg.broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		return m.tunnelSnapshot()
	})
	// A resumed session can carry a canonical tunnel ledger that only reflects
	// the pre-restart projection. Revalidate it before the first mobile attach so
	// broker replay falls back to the current chat snapshot when needed.
	msg.broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return m.currentSessionTunnelReplayEvents()
	})
	msg.broker.SetEventRecorder(nil)
	if m.session != nil && m.session.ID != "" {
		msg.broker.BindSession(m.session.ID)
		msg.broker.SetAuthorityEpoch(m.currentSessionTunnelAuthorityEpoch())
	}
	m.beginTunnelShareBootstrapCapture(msg.generation)

	// Fresh share rooms must contain canonical bootstrap history before older
	// relay/mobile combinations attach, otherwise the client can stall waiting for
	// session_info and later live events can reuse stale event ids. Keep canonical
	// event recording on the projection path, but delay mirroring to the share
	// broker until background bootstrap finishes so Update stays responsive.
	subtitle := "Scan with GGCode Mobile to connect"
	if msg.info.CompatibilityNotice != "" {
		subtitle += " - " + msg.info.CompatibilityNotice
	}
	m.openQROverlayDirect(
		"Mobile Tunnel",
		subtitle,
		msg.info.QRCode,
		msg.info.ConnectURL,
	)
	// Copy share URL to clipboard so the user can paste it elsewhere
	if msg.info.ConnectURL != "" {
		_ = clipboard.WriteAll(msg.info.ConnectURL)
	}

	return m, m.bootstrapTunnelShare(msg.generation)
}

func (m *Model) handleTunnelRefreshMsg(msg tunnelRefreshMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentTunnelGeneration(msg.generation) || msg.session == nil || msg.session != m.tunnelSession {
		return m, nil
	}
	if msg.err != nil {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Tunnel share refresh failed: %v", msg.err))
		m.chatListScrollToBottom()
		return m, nil
	}
	if msg.info == nil {
		m.chatWriteSystem(nextSystemID(), "Tunnel share refresh failed: missing refreshed invite")
		m.chatListScrollToBottom()
		return m, nil
	}
	subtitle := "Scan with GGCode Mobile to connect"
	if msg.info.CompatibilityNotice != "" {
		subtitle += " - " + msg.info.CompatibilityNotice
	}
	m.openQROverlayDirect(
		"Mobile Tunnel",
		subtitle,
		msg.info.QRCode,
		msg.info.ConnectURL,
	)
	// Copy refreshed share URL to clipboard
	if msg.info.ConnectURL != "" {
		_ = clipboard.WriteAll(msg.info.ConnectURL)
	}
	return m, nil
}

func (m *Model) handleTunnelClientConnectedMsg() (tea.Model, tea.Cmd) {
	return m.handleTunnelClientConnectedMsgForGeneration(0)
}

func (m *Model) handleTunnelClientConnectedMsgForGeneration(generation uint64) (tea.Model, tea.Cmd) {
	if !m.isCurrentTunnelGeneration(generation) {
		return m, nil
	}
	if m.tunnelSession == nil {
		return m, nil
	}
	if m.qrOverlay != nil {
		m.closeQROverlay()
	}
	if m.tunnelClientNoticeShown {
		return m, nil
	}
	m.tunnelClientNoticeShown = true
	sysMsg := m.t("tunnel.mobile_connected")
	m.suppressNextTunnelSystem = sysMsg
	m.chatWriteSystem(nextSystemID(), sysMsg)
	m.chatListScrollToBottom()
	return m, nil
}

// ─── Outbound: Agent stream events → mobile ───

func tunnelReasoningMsgIDFor(msgID string) string {
	return agentruntime.TunnelReasoningMsgID(msgID)
}

func (m *Model) ensureTunnelMainStreamState() *tunnelMainStreamState {
	if m.tunnelMainStream == nil {
		m.tunnelMainStream = &tunnelMainStreamState{}
	}
	return m.tunnelMainStream
}

func (m *Model) syncTunnelMainStreamCache(msgID string, needsFinalize bool) {
	// TunnelHost manages stream state; this is a no-op kept for compatibility.
}

func (m *Model) currentTunnelMsgID() string {
	state := m.ensureTunnelMainStreamState()
	state.mu.Lock()
	defer state.mu.Unlock()
	next := agentruntime.EnsureTunnelMainStream(agentruntime.TunnelMainStream{
		MessageID:     state.msgID,
		NeedsFinalize: state.needsFinalize,
	}, m.tunnelEventBroker())
	state.msgID = next.MessageID
	state.needsFinalize = next.NeedsFinalize
	m.syncTunnelMainStreamCache(state.msgID, state.needsFinalize)
	return state.msgID
}

func (m *Model) tunnelReasoningMsgID() string {
	return tunnelReasoningMsgIDFor(m.currentTunnelMsgID())
}

func (m *Model) setTunnelMainStream(msgID string, needsFinalize bool) {
	state := m.ensureTunnelMainStreamState()
	state.mu.Lock()
	state.msgID = msgID
	state.needsFinalize = needsFinalize
	m.syncTunnelMainStreamCache(state.msgID, state.needsFinalize)
	state.mu.Unlock()
}

func (m *Model) resetTunnelMainStream() {
	state := m.ensureTunnelMainStreamState()
	state.mu.Lock()
	state.msgID = ""
	state.needsFinalize = false
	m.syncTunnelMainStreamCache(state.msgID, state.needsFinalize)
	state.mu.Unlock()
}

func (m *Model) markTunnelMainStreamActive() {
	state := m.ensureTunnelMainStreamState()
	state.mu.Lock()
	next := agentruntime.MarkTunnelMainStreamActive(agentruntime.TunnelMainStream{
		MessageID:     state.msgID,
		NeedsFinalize: state.needsFinalize,
	})
	state.msgID = next.MessageID
	state.needsFinalize = next.NeedsFinalize
	m.syncTunnelMainStreamCache(state.msgID, state.needsFinalize)
	state.mu.Unlock()
}

func (m *Model) rolloverTunnelMainStream(force bool) {
	state := m.ensureTunnelMainStreamState()
	state.mu.Lock()
	defer state.mu.Unlock()
	next := agentruntime.FlushTunnelMainStream(agentruntime.TunnelMainStream{
		MessageID:     state.msgID,
		NeedsFinalize: state.needsFinalize,
	}, m.tunnelEventBroker(), force)
	state.msgID = next.MessageID
	state.needsFinalize = next.NeedsFinalize
	m.syncTunnelMainStreamCache(state.msgID, state.needsFinalize)
}

func subagentTunnelTextMsgID(agentID string) string {
	return fmt.Sprintf("sa-%s", agentID)
}

func subagentTunnelReasoningMsgID(agentID string) string {
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func (m *Model) tunnelEventBroker() *tunnel.Broker {
	if m.tunnelHost != nil {
		return m.tunnelHost.ProjectionBroker()
	}
	return nil
}

func (m *Model) ensureTunnelProjectionBroker() *tunnel.Broker {
	return m.tunnelEventBroker()
}

func (m *Model) bindTunnelProjectionSession() {
	if m.session == nil || strings.TrimSpace(m.session.ID) == "" {
		return
	}
	if m.tunnelHost != nil {
		m.tunnelHost.BindSession(m.session, m.sessionStore)
	}
}

func (m *Model) ensureProjectionBootstrap(broker *tunnel.Broker, replay []tunnel.GatewayMessage) {
	if broker == nil || len(replay) == 0 {
		return
	}
	snapshot := m.tunnelSnapshot()
	if !projectionReplayHasType(replay, tunnel.EventSessionInfo) && snapshot.SessionInfo != (tunnel.SessionInfoData{}) {
		broker.SendSessionInfo(snapshot.SessionInfo)
	}
	if !projectionReplayHasType(replay, tunnel.EventStatus) && snapshot.Status.Status != "" {
		broker.PushStatus(snapshot.Status.Status, snapshot.Status.Message)
	}
	if !projectionReplayHasType(replay, tunnel.EventActivity) && snapshot.Activity.Activity != "" {
		broker.PushActivity(snapshot.Activity.Activity)
	}
}

func projectionReplayHasType(events []tunnel.GatewayMessage, eventType string) bool {
	for _, ev := range events {
		if ev.Type == eventType {
			return true
		}
	}
	return false
}

func (m *Model) currentSessionTunnelAuthorityEpoch() uint64 {
	if m.tunnelHost != nil {
		return m.tunnelHost.AuthorityEpoch()
	}
	return 1
}

func (m *Model) hydrateProjectionReplayFromSessionLedger(store *tunnel.ProjectionStore, replay []tunnel.GatewayMessage) []tunnel.GatewayMessage {
	updated, err := agentruntime.HydrateProjectionReplayFromSessionLedger(store, m.session, replay)
	if err != nil {
		sessionID := ""
		if m.session != nil {
			sessionID = m.session.ID
		}
		debug.Log("tunnel", "projection: hydrate from session ledger failed for %s: %v", sessionID, err)
		return replay
	}
	return updated
}

func (m *Model) tunnelHostProjectionStore() *tunnel.ProjectionStore {
	if m.tunnelHost != nil {
		return m.tunnelHost.ProjectionStore()
	}
	return nil
}

func (m *Model) recordProjectionEvent(ev tunnel.GatewayMessage) {
	// TunnelHost handles projection store + session recording + online forwarding.
	// Keep only TUI-specific bootstrap capture.
	m.captureTunnelShareBootstrapEvent(ev)
}

// pushTunnelEvent pushes a provider stream event to the mobile client.
// Called from the agent stream callback in submit.go. Nil-safe.
func (m *Model) pushTunnelEvent(ev provider.StreamEvent) {
	// Use unified TunnelHost if available
	if m.tunnelHost != nil {
		m.tunnelHost.PushStreamEvent(ev)
		return
	}

	// Legacy fallback
	broker := m.tunnelEventBroker()
	if broker == nil {
		return
	}

	switch ev.Type {
	case provider.StreamEventText:
		msgID := m.currentTunnelMsgID()
		if msgID == "" {
			return
		}
		m.markTunnelMainStreamActive()
		broker.PushReasoningDone(tunnelReasoningMsgIDFor(msgID))
		broker.PushText(msgID, ev.Text)

	case provider.StreamEventReasoning:
		if chunk := tunnel.NormalizeReasoningChunk(ev.Text); chunk != "" {
			msgID := m.currentTunnelMsgID()
			if msgID == "" {
				return
			}
			m.markTunnelMainStreamActive()
			broker.PushReasoning(tunnelReasoningMsgIDFor(msgID), chunk)
		}

	case provider.StreamEventToolCallDone:
		m.rolloverTunnelMainStream(true)
		name := ev.Tool.Name
		if name == "" {
			name = "tool"
		}
		present := describeTool(m.currentLanguage(), name, string(ev.Tool.Arguments))
		title := toolCallDisplayName(name, string(ev.Tool.Arguments))
		broker.PushToolCall(ev.Tool.ID, name, title, string(ev.Tool.Arguments), present.Detail)

	case provider.StreamEventToolResult:
		m.rolloverTunnelMainStream(false)
		content := ev.Result
		if len([]rune(content)) > 2000 {
			content = truncateRunes(content, 2000, "\n...(truncated)")
		}
		m.pushTunnelToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventSystem:
		m.rolloverTunnelMainStream(true)

	case provider.StreamEventDone:
		m.rolloverTunnelMainStream(true)

	case provider.StreamEventError:
		m.rolloverTunnelMainStream(true)
		if ev.Error != nil {
			broker.PushError(sanitizeAPIError(ev.Error).Error())
		}
	}
}

// pushTunnelUserMessage echoes a locally-typed user message to the mobile client.
func (m *Model) pushTunnelUserMessage(text string) {
	if m.tunnelHost != nil {
		if m.tunnelUserMessageOverride != nil {
			override := *m.tunnelUserMessageOverride
			if override.Text == "" {
				override.Text = text
			}
			m.tunnelUserMessageOverride = nil
			m.tunnelHost.PushUserMessageData(override)
			return
		}
		m.tunnelHost.PushUserMessage(text)
		return
	}

	// Legacy fallback
	if broker := m.tunnelEventBroker(); broker != nil {
		if m.tunnelUserMessageOverride != nil {
			override := *m.tunnelUserMessageOverride
			if override.Text == "" {
				override.Text = text
			}
			m.tunnelUserMessageOverride = nil
			broker.PushUserMessageData(override)
			return
		}
		broker.PushUserMessage(text)
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
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushToolResult(toolID, toolName, result, isError)
	}
}

// pushTunnelStatus sends a main-agent status update to the mobile client.
func (m *Model) pushTunnelStatus(status, message string) {
	if m.tunnelHost != nil {
		m.tunnelHost.PushStatus(status, message)
		return
	}
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushStatus(status, message)
	}
}

func (m *Model) pushTunnelActivity(activity string) {
	if m.tunnelHost != nil {
		m.tunnelHost.PushActivity(activity)
		return
	}
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushActivity(strings.TrimSpace(activity))
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
	if broker := m.tunnelEventBroker(); broker != nil {
		m.rolloverTunnelMainStream(true)
		m.pushTunnelStatus(tunnel.StatusIdle, "cancelled")
		m.pushTunnelActivity("")
	}
}

// ─── Outbound: Sub-agent events → mobile ───

// pushSubAgentTunnelEvent pushes sub-agent lifecycle events to the mobile client.
func (m *Model) pushSubAgentTunnelEvent(sa *subagent.SubAgent) {
	broker := m.tunnelEventBroker()
	if broker == nil {
		return
	}
	if m.tunnelSpawned == nil {
		m.tunnelSpawned = make(map[string]bool)
	}

	switch sa.Status {
	case subagent.StatusRunning:
		if !m.tunnelSpawned[sa.ID] {
			m.tunnelSpawned[sa.ID] = true
			broker.PushSubagentSpawn(sa.ID, sa.Name, sa.Task, "", "")
		}
		broker.PushSubagentStatus(sa.ID, tunnel.StatusRunning, sa.CurrentTool)

	case subagent.StatusCompleted:
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(sa.ID))
		if sa.Result != "" {
			msgID := subagentTunnelTextMsgID(sa.ID)
			broker.PushSubagentText(sa.ID, msgID, sa.Result, true)
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, sa.Result, true)

	case subagent.StatusFailed:
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(sa.ID))
		errMsg := ""
		if sa.Error != nil {
			errMsg = sa.Error.Error()
		}
		broker.PushSubagentComplete(sa.ID, sa.Name, errMsg, false)

	case subagent.StatusCancelled:
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(sa.ID))
		broker.PushSubagentComplete(sa.ID, sa.Name, "cancelled", false)
	}
}

// pushSubAgentTunnelStreamText pushes streaming text from a sub-agent.
func (m *Model) pushSubAgentTunnelStreamText(agentID, text string) {
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(agentID))
		msgID := subagentTunnelTextMsgID(agentID)
		broker.PushSubagentText(agentID, msgID, text, false)
	}
}

func (m *Model) pushSubAgentTunnelReasoning(agentID, text string) {
	broker := m.tunnelEventBroker()
	if broker == nil {
		return
	}
	if chunk := tunnel.NormalizeReasoningChunk(text); chunk != "" {
		broker.PushSubagentReasoning(agentID, subagentTunnelReasoningMsgID(agentID), chunk, false)
	}
}

// pushSubAgentTunnelToolCall pushes a tool call from a sub-agent.
func (m *Model) pushSubAgentTunnelToolCall(agentID, toolID, toolName, displayName, args, detail string) {
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(agentID))
		broker.PushSubagentToolCall(agentID, toolID, toolName, displayName, args, detail)
	}
}

// pushSubAgentTunnelToolResult pushes a tool result from a sub-agent.
func (m *Model) pushSubAgentTunnelToolResult(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
	if broker := m.tunnelEventBroker(); broker != nil {
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(agentID))
		broker.PushSubagentToolResult(agentID, toolID, toolName, displayName, detail, result, isError)
	}
}

// ─── Outbound: Swarm events → mobile ───

// pushSwarmTunnelEvent pushes swarm/teammate events to the mobile client.
func (m *Model) pushSwarmTunnelEvent(ev swarm.Event) {
	broker := m.tunnelEventBroker()
	if broker == nil {
		return
	}

	switch ev.Type {
	case "teammate_tool_call":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
		detail := describeTool(LangEnglish, ev.CurrentTool, ev.ToolArgs).Detail
		title := toolCallDisplayName(ev.CurrentTool, ev.ToolArgs)
		broker.PushSubagentToolCall(ev.TeammateID, ev.ToolID, ev.CurrentTool, title, ev.ToolArgs, detail)
		broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.CurrentTool)

	case "teammate_tool_result":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
		broker.PushSubagentToolResult(ev.TeammateID, ev.ToolID, ev.CurrentTool, "", "", ev.ToolArgs, ev.IsError)

	case "teammate_reasoning":
		if chunk := tunnel.NormalizeReasoningChunk(ev.Result); chunk != "" {
			broker.PushSubagentReasoning(ev.TeammateID, subagentTunnelReasoningMsgID(ev.TeammateID), chunk, false)
		}

	case "teammate_text":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
		msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
		broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, false)

	case "teammate_spawned":
		color := ""
		if m.swarmMgr != nil {
			if snap, ok := m.swarmMgr.TeammateSnapshot(ev.TeammateID); ok {
				color = snap.Color
			}
		}
		broker.PushSubagentSpawn(ev.TeammateID, ev.TeammateName, "teammate", color, ev.TeamID)

	case "teammate_working":
		broker.PushSubagentStatus(ev.TeammateID, tunnel.StatusRunning, ev.TeammateName)
		if m.swarmMgr != nil {
			if snap, ok := m.swarmMgr.TeammateSnapshot(ev.TeammateID); ok && len(snap.Events) > 0 {
				last := snap.Events[len(snap.Events)-1]
				if last.Type == swarm.TeammateEventText && last.Text != "" {
					msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
					broker.PushSubagentText(ev.TeammateID, msgID, last.Text, false)
				}
			}
		}

	case "teammate_idle":
		broker.PushReasoningDone(subagentTunnelReasoningMsgID(ev.TeammateID))
		if ev.Result != "" {
			msgID := fmt.Sprintf("tm-%s", ev.TeammateID)
			broker.PushSubagentText(ev.TeammateID, msgID, ev.Result, true)
		}
		success := ev.Error == nil
		summary := ev.Result
		if ev.Error != nil {
			summary = ev.Error.Error()
		}
		broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, summary, success)

	case "teammate_shutdown":
		broker.PushSubagentComplete(ev.TeammateID, ev.TeammateName, "shutdown", true)
	}
}

// ─── Inbound: Mobile → agent ───

// handleTunnelClientCommand is called from the broker's OnCommand callback
// (runs on a goroutine). It routes mobile commands into the Bubble Tea event loop.
func (m *Model) handleTunnelClientCommand(generation uint64, broker *tunnel.Broker, cmd tunnel.GatewayMessage) {
	agentruntime.RouteTunnelCommand(cmd, agentruntime.TunnelCommandHooks{
		OnUserMessage: func(data tunnel.MessageData) {
			if m.program != nil {
				m.program.Send(tunnelInboundMsg{
					generation: generation,
					text:       data.Text,
					messageID:  data.MessageID,
				})
			}
		},
		OnInterrupt: func() {
			if m.program != nil {
				m.program.Send(tunnelInboundMsg{generation: generation, text: "/interrupt"})
			}
		},
		OnModeChange: func(data tunnel.ModeChangeData) {
			if m.program != nil {
				m.program.Send(tunnelModeChangeMsg{generation: generation, mode: data.Mode})
			}
		},
		OnApprovalResponse: func(data tunnel.ApprovalResponseData) {
			if m.program != nil {
				m.program.Send(tunnelApprovalResponseMsg{generation: generation, id: data.ID, decision: data.Decision})
			}
		},
		OnAskUserResponse: func(data tunnel.AskUserResponseData) {
			if m.program != nil {
				m.program.Send(tunnelAskUserResponseMsg{generation: generation, id: data.ID, status: data.Status, answers: data.Answers})
			}
		},
		OnLanguageChange: func(data tunnel.LanguageChangeData) {
			if m.program != nil {
				m.program.Send(tunnelLanguageChangeMsg{generation: generation, language: data.Language})
			}
		},
		OnThemeChange: func(data tunnel.ThemeChangeData) {
			if m.program != nil {
				m.program.Send(tunnelThemeChangeMsg{generation: generation, theme: data.Theme})
			}
		},
		OnServerAck: func(messageID string) {
			if broker != nil {
				broker.PushServerAck(messageID)
			}
		},
	})
}

// handleTunnelInboundMsg processes a user message from the mobile client.
// It routes through the same idle→startAgent / busy→queuePendingSubmission
// path as webchat messages.
func (m *Model) handleTunnelInboundMsg(msg tunnelInboundMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
	text := msg.text
	if text == "" {
		return m, nil
	}

	// Handle interrupt specially.
	if text == "/interrupt" {
		m.cancelActiveRun()
		return m, nil
	}
	if msg.messageID != "" {
		m.setNextTunnelUserMessageOverride(tunnel.MessageData{
			Text:      text,
			MessageID: msg.messageID,
		})
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
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
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
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
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
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
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
			if reasoning := strings.TrimSpace(it.Reasoning()); reasoning != "" {
				history = append(history, tunnel.HistoryEntry{Role: "reasoning", Content: reasoning})
			}
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
			Status() chat.ToolStatus
		}:
			rawArgs := it.Input()
			present := describeTool(m.currentLanguage(), it.ToolName(), rawArgs)
			argsStr := truncateRunes(rawArgs, 200, "...")
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          it.ID(),
				ToolName:        it.ToolName(),
				ToolDisplayName: present.DisplayName,
				ToolArgs:        argsStr,
				ToolDetail:      present.Detail,
			})
			result := strings.TrimSpace(it.Result())
			if result != "" || isTerminalToolStatus(it.Status()) {
				history = append(history, tunnel.HistoryEntry{
					Role:     "tool_result",
					ToolID:   it.ID(),
					ToolName: it.ToolName(),
					Result:   result,
					IsError:  it.IsError(),
				})
			}
		case *chat.AgentToolItem:
			status := it.Status()
			history = append(history, tunnel.HistoryEntry{
				Role:            "tool_call",
				ToolID:          it.ID(),
				ToolName:        "spawn_agent",
				ToolDisplayName: it.Label(),
			})
			result := strings.TrimSpace(it.Result())
			if result != "" || isTerminalToolStatus(status) {
				history = append(history, tunnel.HistoryEntry{
					Role:     "tool_result",
					ToolID:   it.ID(),
					ToolName: "spawn_agent",
					Result:   result,
					IsError:  status == chat.StatusError || status == chat.StatusCanceled,
				})
			}
		}
	}
	return history
}

func isTerminalToolStatus(status chat.ToolStatus) bool {
	switch status {
	case chat.StatusSuccess, chat.StatusError, chat.StatusCanceled:
		return true
	default:
		return false
	}
}

func contentBlockReasoningText(block provider.ContentBlock) string {
	if text := tunnel.NormalizeReasoningChunk(block.ReasoningContent); text != "" {
		return text
	}
	if strings.TrimSpace(block.ThinkingData) != "" {
		return tunnel.RedactedReasoningPlaceholder
	}
	return ""
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
					result := truncateRunes(block.Output, 500, "...")
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
				if reasoning := contentBlockReasoningText(block); reasoning != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "reasoning",
						Content: reasoning,
					})
				}
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "assistant",
						Content: strings.TrimSpace(block.Text),
					})
				} else if block.Type == "tool_use" {
					argsStr := truncateRunes(string(block.Input), 200, "...")
					present := describeTool(LangEnglish, block.ToolName, string(block.Input))
					history = append(history, tunnel.HistoryEntry{
						Role:            "tool_call",
						ToolID:          block.ToolID,
						ToolName:        block.ToolName,
						ToolDisplayName: present.DisplayName,
						ToolArgs:        argsStr,
						ToolDetail:      present.Detail,
					})
				}
			}
		case "tool":
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					result := truncateRunes(block.Output, 500, "...")
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
		case swarm.TeammateEventReasoning:
			converted.Type = subagent.AgentEventReasoning
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
	reasoningBuf := strings.Builder{}
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
	flushReasoning := func(done bool) {
		if reasoningBuf.Len() == 0 {
			return
		}
		reasoningID := subagentTunnelReasoningMsgID(agentID)
		out = append(out, snapshotEvent(
			tunnel.EventSubagentReasoning,
			reasoningID,
			tunnel.SubagentReasoningData{AgentID: agentID, ID: reasoningID, Chunk: reasoningBuf.String()},
		))
		if done {
			out = append(out, snapshotEvent(
				tunnel.EventSubagentReasoningDone,
				reasoningID,
				tunnel.SubagentReasoningData{AgentID: agentID, ID: reasoningID, Done: true},
			))
		}
		reasoningBuf.Reset()
	}
	for _, ev := range events {
		switch ev.Type {
		case subagent.AgentEventReasoning:
			if ev.Text != "" {
				reasoningBuf.WriteString(tunnel.NormalizeReasoningChunk(ev.Text))
			}
		case subagent.AgentEventToolCall:
			flushReasoning(true)
			flushText(false)
			if ev.ToolID != "" {
				toolArgsByID[ev.ToolID] = ev.ToolArgs
			}
			displayName := ev.ToolDisplayName
			if displayName == "" {
				displayName = toolCallDisplayName(ev.ToolName, ev.ToolArgs)
			}
			detail := ev.ToolDetail
			if detail == "" {
				detail = describeTool(LangEnglish, ev.ToolName, ev.ToolArgs).Detail
			}
			out = append(out, snapshotEvent(
				tunnel.EventSubagentToolCall,
				agentID,
				tunnel.SubagentToolCallData{
					AgentID:     agentID,
					ToolID:      ev.ToolID,
					ToolName:    ev.ToolName,
					DisplayName: displayName,
					Args:        ev.ToolArgs,
					Detail:      detail,
				},
			))
		case subagent.AgentEventToolResult:
			flushReasoning(true)
			flushText(false)
			rawArgs := ev.ToolArgs
			if rawArgs == "" {
				rawArgs = toolArgsByID[ev.ToolID]
			}
			present, _ := toolpkg.DescribeToolResult(ev.ToolName, rawArgs, ev.Result, ev.IsError)
			delete(toolArgsByID, ev.ToolID)
			out = append(out, snapshotEvent(
				tunnel.EventSubagentToolResult,
				agentID,
				tunnel.SubagentToolResultData{
					AgentID:     agentID,
					ToolID:      ev.ToolID,
					ToolName:    ev.ToolName,
					DisplayName: ev.ToolDisplayName,
					Detail:      ev.ToolDetail,
					Result:      ev.Result,
					Summary:     present.Summary,
					Payload:     present.Payload,
					PayloadMode: present.PayloadMode,
					IsError:     ev.IsError,
				},
			))
		case subagent.AgentEventError:
			flushReasoning(true)
			if ev.Text != "" {
				if textBuf.Len() > 0 {
					textBuf.WriteString("\n")
				}
				textBuf.WriteString(ev.Text)
			}
		default:
			flushReasoning(true)
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
	flushReasoning(true)
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

func (m *Model) bootstrapTunnelShare(generation uint64) tea.Cmd {
	return func() tea.Msg {
		if !m.isCurrentTunnelGeneration(generation) {
			return tunnelShareBootstrapMsg{generation: generation}
		}
		m.prepareCurrentSessionTunnelLedger()
		if broker := m.tunnelEventBroker(); broker != nil {
			events := m.currentSessionTunnelReplayEvents()
			if len(events) == 0 {
				broker.SendSnapshot(m.tunnelSnapshot())
			} else {
				m.ensureProjectionBootstrap(broker, events)
			}
		}
		return tunnelShareBootstrapMsg{generation: generation}
	}
}

func (m *Model) handleTunnelShareBootstrapMsg(msg tunnelShareBootstrapMsg) (tea.Model, tea.Cmd) {
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
	if m.tunnelBroker == nil {
		m.cancelTunnelShareBootstrapCapture()
		return m, nil
	}

	epoch := m.currentSessionTunnelAuthorityEpoch()
	if epoch != 0 {
		m.tunnelBroker.SetAuthorityEpoch(epoch)
	}

	seen := make(map[string]struct{})
	replayed := false
	if events := m.currentSessionTunnelReplayEvents(); len(events) > 0 {
		m.tunnelBroker.ReplayEvents(events, false)
		replayed = true
		for _, ev := range events {
			if ev.EventID != "" {
				seen[ev.EventID] = struct{}{}
			}
		}
	}

	pending := m.finishTunnelShareBootstrapCapture(msg.generation)
	if len(seen) > 0 {
		filtered := pending[:0]
		for _, ev := range pending {
			if ev.EventID != "" {
				if _, ok := seen[ev.EventID]; ok {
					continue
				}
			}
			filtered = append(filtered, ev)
		}
		pending = filtered
	}
	if len(pending) > 0 {
		m.tunnelBroker.ReplayEvents(pending, false)
		replayed = true
	}
	if !replayed {
		m.tunnelBroker.SendSnapshot(m.tunnelSnapshot())
	}
	if m.session != nil && m.session.ID != "" {
		m.tunnelBroker.AnnounceActiveSession(m.session.ID)
	}
	return m, nil
}

func (m *Model) publishTunnelSnapshotForCurrentSessionWithReport(reset bool) (tunnel.BrokerSnapshot, bool) {
	if m.tunnelBroker == nil {
		return m.tunnelSnapshot(), false
	}
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	m.prepareCurrentSessionTunnelLedger()
	if events := m.currentSessionTunnelReplayEvents(); len(events) > 0 {
		agentruntime.PublishShareState(m.tunnelBroker, sessionID, tunnel.BrokerSnapshot{}, events, reset)
		return tunnel.BrokerSnapshot{}, true
	}
	snapshot := m.tunnelSnapshot()
	agentruntime.PublishShareState(m.tunnelBroker, sessionID, snapshot, nil, reset)
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
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.ID
	}
	agentruntime.PublishShareState(m.tunnelBroker, sessionID, latest, nil, true)
}

func (m *Model) currentSessionTunnelReplayEvents() []tunnel.GatewayMessage {
	if m.tunnelHost != nil {
		return m.tunnelHost.TunnelEvents()
	}
	return nil
}

func tunnelEventsToHistory(events []session.TunnelEvent) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	textByID := make(map[string]string)
	textKindByID := make(map[string]string)
	reasoningByID := make(map[string]string)
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
	finalizeReasoning := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		text := strings.TrimSpace(reasoningByID[id])
		delete(reasoningByID, id)
		if text == "" {
			return
		}
		history = append(history, tunnel.HistoryEntry{
			Role:    "reasoning",
			Content: text,
		})
	}
	finalizeAllReasoning := func() {
		if len(reasoningByID) == 0 {
			return
		}
		ids := make([]string, 0, len(reasoningByID))
		for id := range reasoningByID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			finalizeReasoning(id)
		}
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
			if _, seen := textByID[data.ID]; !seen {
				finalizeAllReasoning()
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
		case tunnel.EventReasoning:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			if strings.TrimSpace(data.ID) == "" || data.Chunk == "" {
				continue
			}
			reasoningByID[data.ID] += data.Chunk
			if data.Done {
				finalizeReasoning(data.ID)
			}
		case tunnel.EventReasoningDone:
			var data tunnel.TextData
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				continue
			}
			id := data.ID
			if strings.TrimSpace(id) == "" {
				id = ev.StreamID
			}
			finalizeReasoning(id)
		case tunnel.EventToolCall:
			finalizeAllReasoning()
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
			finalizeAllReasoning()
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
			finalizeAllReasoning()
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

	for id := range reasoningByID {
		finalizeReasoning(id)
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
			a[i].Kind != b[i].Kind ||
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
	projectionStore := m.tunnelHostProjectionStore()
	m.sessionMutex().Unlock()

	_ = store.Save(ses)
	if projectionStore != nil {
		if epoch, err := projectionStore.CutAuthority(ses.ID); err == nil {
			if m.tunnelEventBroker() != nil {
				m.tunnelEventBroker().SetAuthorityEpoch(epoch)
			}
			if m.tunnelBroker != nil {
				m.tunnelBroker.SetAuthorityEpoch(epoch)
			}
		}
	}
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
	projectionStore := m.tunnelHostProjectionStore()
	m.sessionMutex().Unlock()

	_ = store.Save(ses)
	if projectionStore != nil {
		if epoch, err := projectionStore.CutAuthority(ses.ID); err == nil {
			if m.tunnelEventBroker() != nil {
				m.tunnelEventBroker().SetAuthorityEpoch(epoch)
			}
			if m.tunnelBroker != nil {
				m.tunnelBroker.SetAuthorityEpoch(epoch)
			}
		}
	}
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
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
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
	if !m.isCurrentTunnelGeneration(msg.generation) {
		return m, nil
	}
	qs := m.pendingQuestionnaire
	if qs == nil {
		return m, nil
	}
	if m.tunnelPendingAskUserID != "" && msg.id != "" && msg.id != m.tunnelPendingAskUserID {
		return m, nil
	}

	result := buildAskUserResponseFromTunnel(qs.request, msg.status, msg.answers)
	respCh := qs.response
	m.pendingQuestionnaire = nil
	m.tunnelPendingAskUserID = ""

	if respCh != nil {
		select {
		case respCh <- result:
		default:
		}
	}

	// Send status update to mobile
	m.pushTunnelCurrentStatus()

	return m, nil
}

func (m *Model) nextTunnelRequestID() string {
	if broker := m.tunnelEventBroker(); broker == nil {
		return ""
	} else {
		return broker.NextMessageID()
	}
}

func (m *Model) pushTunnelApprovalResult(id, decision string) {
	broker := m.tunnelEventBroker()
	if broker == nil || strings.TrimSpace(id) == "" {
		return
	}
	status := m.currentTunnelStatus()
	agentruntime.PushTunnelApprovalResult(broker, id, decision, agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      status.Status,
		StatusMsg:   status.Message,
		HasActivity: true,
		Activity:    m.currentTunnelActivity(),
	})
}

func (m *Model) pushTunnelAskUserResponse(id string, response toolpkg.AskUserResponse) {
	broker := m.tunnelEventBroker()
	if broker == nil || strings.TrimSpace(id) == "" {
		return
	}
	status := m.currentTunnelStatus()
	agentruntime.PushTunnelAskUserResponse(broker, id, response, agentruntime.TunnelStateUpdate{
		HasStatus:   true,
		Status:      status.Status,
		StatusMsg:   status.Message,
		HasActivity: true,
		Activity:    m.currentTunnelActivity(),
	})
}

func tunnelDecisionString(decision permission.Decision) string {
	return agentruntime.TunnelDecisionFromApproval(decision)
}

func buildAskUserResponseFromTunnel(req toolpkg.AskUserRequest, status string, answers []tunnel.AskUserAnswer) toolpkg.AskUserResponse {
	return agentruntime.BuildAskUserResponseFromTunnel(req, status, answers)
}
