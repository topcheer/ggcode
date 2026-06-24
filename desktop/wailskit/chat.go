// Package wailskit provides a public facade for the Wails desktop app.
package wailskit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/acpclient"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/relaycatalog"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/uiusage"
)

func displayReasoningEffort(effort string) string {
	if strings.TrimSpace(effort) == "" {
		return "auto"
	}
	return strings.TrimSpace(effort)
}

var reasoningEffortCycle = []string{"", "low", "medium", "high"}

func nextReasoningEffort(current string) string {
	current = strings.ToLower(strings.TrimSpace(current))
	for i, effort := range reasoningEffortCycle {
		if current == effort {
			return reasoningEffortCycle[(i+1)%len(reasoningEffortCycle)]
		}
	}
	return reasoningEffortCycle[1]
}

type MCPOAuthStartResult struct {
	ServerName     string `json:"serverName"`
	AuthorizeURL   string `json:"authorizeUrl"`
	DeviceUserCode string `json:"deviceUserCode,omitempty"`
	OpenError      string `json:"openError,omitempty"`
}

// ChatBridge manages the full agent chat loop for the Wails frontend,
// mirroring the Fyne desktop's AgentBridge tool registration and session management.
type ChatBridge struct {
	cfg            *config.Config
	resolved       *config.ResolvedEndpoint
	agent          *agent.Agent
	registry       *tool.Registry
	mcpManager     *plugin.MCPManager
	workingDir     string
	sessionStore   session.Store
	currentSes     *session.Session
	permissionMode permission.PermissionMode

	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	startTime time.Time

	// Pending messages (mirrors Fyne pendingMsgs)
	pendingMsgs *agentruntime.PendingQueue[*tunnel.MessageData]

	// Subsystems
	cronScheduler *cron.Scheduler
	subAgentMgr   *subagent.Manager
	acpClientMgr  *acpclient.ClientManager
	swarmMgr      *swarm.Manager

	// Metrics
	metricCancel         context.CancelFunc
	metricCollector      *metrics.Collector
	metricEvents         []metrics.MetricEvent
	usageTurnIndex       int
	lastMetricDigestTurn int
	pendingDigests       []provider.Message
	desktopTurnCounter   int64
	desktopTurnID        string
	desktopAssistantID   string
	desktopTextSeq       int

	// UI event emitter — set by app.go via SetEmitEvent
	EmitEvent func(name string, payload ...interface{})

	// Sub-agent tunnel tracking
	spawnedSet map[string]bool

	// IM outbound push — same as Fyne agentBridge.Emitter
	Emitter *im.IMEmitter

	// IM round accumulator for emitter (mirrors Fyne agentBridge.imRound)
	imRound agentruntime.IMRoundState

	// Unified tunnel event management (from InteractiveRuntimeCore.Tunnel)
	tunnelHost *agentruntime.TunnelHost

	// A2A server for agent-to-agent communication on LAN
	a2aServer        *a2a.Server
	a2aRegistry      *a2a.Registry
	a2aRemoteTool    *a2a.RemoteTool
	a2aRefreshCancel context.CancelFunc
	lanchatHub       *lanchat.Hub

	// Pending approval/ask_user requests from agent
	interactions *agentruntime.InteractionBroker

	// Callback for emitting events to frontend.
	OnStreamEvent func(eventType string, data json.RawMessage)

	// Callback fired after current session changes so the host can bind IM/runtime state.
	OnSessionChanged func()

	liveHistory []SessionMessage

	// Session lock for preventing concurrent access to the same session.
	sessionLock      *session.SessionLock
	sessionEphemeral bool // true if this session should be deleted when empty
}

// NewChatBridge creates a new chat bridge using the global config.
func NewChatBridge() (*ChatBridge, error) {
	cfg := GetGlobalConfig()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	wd, _ := os.Getwd()
	modeStr := cfg.DefaultMode
	if modeStr == "" {
		modeStr = "auto"
	}
	return &ChatBridge{
		cfg:            cfg,
		workingDir:     wd,
		permissionMode: permission.ParsePermissionMode(modeStr),
		pendingMsgs:    agentruntime.NewPendingQueue[*tunnel.MessageData](),
		interactions:   agentruntime.NewInteractionBroker(),
	}, nil
}

func (b *ChatBridge) GetTeamBoard() []swarm.TeamBoardSnapshot {
	if b == nil || b.swarmMgr == nil {
		return []swarm.TeamBoardSnapshot{}
	}
	return b.swarmMgr.ListTeamBoards()
}

func shouldEmitSwarmBoardUpdate(eventType string) bool {
	switch eventType {
	case "team_created", "team_deleted", "teammate_spawned", "teammate_working", "teammate_idle", "teammate_shutdown", "teammate_error", "team_board_updated":
		return true
	default:
		return false
	}
}

// SetTunnelHost sets the unified tunnel host from InteractiveRuntimeCore.Tunnel.
func (b *ChatBridge) SetTunnelHost(th *agentruntime.TunnelHost) {
	b.tunnelHost = th
}

// GetTunnelHost returns the tunnel host (for StartShare).
func (b *ChatBridge) GetTunnelHost() *agentruntime.TunnelHost {
	return b.tunnelHost
}

func (b *ChatBridge) startDesktopTurnLocked() (turnID, assistantID string) {
	b.desktopTurnCounter++
	turnID = fmt.Sprintf("turn-%d", b.desktopTurnCounter)
	assistantID = fmt.Sprintf("assistant-%s", turnID)
	b.desktopTurnID = turnID
	b.desktopAssistantID = assistantID
	b.desktopTextSeq = 0
	return turnID, assistantID
}

func (b *ChatBridge) desktopTurnSnapshot() (turnID, assistantID string, seq int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.desktopTurnID == "" {
		b.startDesktopTurnLocked()
	}
	b.desktopTextSeq++
	return b.desktopTurnID, b.desktopAssistantID, b.desktopTextSeq
}

// SendMessage sends a user message and streams events to the frontend.
// If agent is already running, queues the message for processing after the current turn.
func (b *ChatBridge) SendMessage(userMsg string) error {
	return b.sendMessageData(tunnel.MessageData{Text: userMsg}, "desktop", "")
}

// LanChatParticipants returns all known LAN chat participants.
func (b *ChatBridge) LanChatParticipants() ([]lanchat.Participant, error) {
	if b.lanchatHub == nil {
		return nil, fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.Participants(), nil
}

// LanChatMessages returns recent messages.
func (b *ChatBridge) LanChatMessages() ([]lanchat.Message, error) {
	if b.lanchatHub == nil {
		return nil, fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.Messages(), nil
}

// LanChatSend broadcasts or sends a direct message.
// If toNodeID is empty, broadcasts to all peers. If toRole is "agent", sends to agent.
func (b *ChatBridge) LanChatSend(content, toNodeID, toRole string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	ctx := context.Background()
	if toNodeID == "" {
		return b.lanchatHub.SendBroadcast(ctx, content, nil)
	}
	return b.lanchatHub.SendDirect(ctx, toNodeID, toRole, content, nil)
}

// LanChatSetNick changes the user's nickname.
func (b *ChatBridge) LanChatSetNick(nick string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.SetNick(nick)
}

// LanChatPendingApprovals returns messages awaiting host approval.
func (b *ChatBridge) LanChatPendingApprovals() ([]lanchat.PendingAgentMsg, error) {
	if b.lanchatHub == nil {
		return nil, fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.PendingApprovals(), nil
}

// LanChatApprove approves a pending @agent message.
func (b *ChatBridge) LanChatApprove(messageID string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	msg, err := b.lanchatHub.ApproveMessage(messageID)
	if err != nil {
		return err
	}
	// Inject into agent loop
	if msg != nil {
		agentText := fmt.Sprintf("[LAN Chat from %s]: %s", msg.FromNick, msg.Content)
		return b.SendMessage(agentText)
	}
	return nil
}

// LanChatReject rejects a pending @agent message.
func (b *ChatBridge) LanChatReject(messageID, reason string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.RejectMessage(messageID, reason)
}

// LanChatSelf returns this node's own participant info.
func (b *ChatBridge) LanChatSelf() (lanchat.Participant, error) {
	if b.lanchatHub == nil {
		return lanchat.Participant{}, fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.SelfParticipant(), nil
}

// SendNonUIMessage sends a user message originating from a non-desktop source (IM/mobile).
// It pushes a user_message event to the frontend so the message appears in the chat,
// but avoids duplicate display on the originating surface.
// excludeAdapter is the IM adapter name to exclude from echo (prevents IM self-echo).
func (b *ChatBridge) SendNonUIMessage(userMsg string, source string, excludeAdapter string) error {
	return b.sendMessageData(tunnel.MessageData{Text: userMsg}, source, excludeAdapter)
}

func (b *ChatBridge) HandleTunnelUserMessage(data tunnel.MessageData) error {
	if strings.TrimSpace(data.Text) == "" {
		return nil
	}
	data.MessageID = tunnel.NormalizeClientMessageID(data.MessageID)
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessageData(data)
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}
	return b.sendMessageData(data, "mobile", "")
}

func (b *ChatBridge) BindShareCommands(broker *tunnel.Broker, onLanguage func(string), currentAskUserRequest func() tool.AskUserRequest, clearAskUserRequest func()) {
	if broker == nil {
		return
	}
	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		agentruntime.RouteTunnelCommand(cmd, agentruntime.TunnelCommandHooks{
			OnUserMessage: func(data tunnel.MessageData) {
				_ = b.HandleTunnelUserMessage(data)
			},
			OnApprovalResponse: func(data tunnel.ApprovalResponseData) {
				b.HandleMobileApprovalResponse(data)
			},
			OnAskUserResponse: func(data tunnel.AskUserResponseData) {
				req := tool.AskUserRequest{}
				if currentAskUserRequest != nil {
					req = currentAskUserRequest()
				}
				b.HandleMobileAskUserResponse(data, req)
				if clearAskUserRequest != nil {
					clearAskUserRequest()
				}
			},
			OnInterrupt: func() {
				b.Cancel()
			},
			OnLanguageChange: func(data tunnel.LanguageChangeData) {
				if onLanguage != nil {
					onLanguage(data.Language)
				}
			},
			OnServerAck: func(messageID string) {
				broker.PushServerAck(messageID)
			},
		})
	})
}

func (b *ChatBridge) sendMessageData(data tunnel.MessageData, source string, excludeAdapter string) error {
	userMsg := strings.TrimSpace(data.Text)
	if userMsg == "" {
		return nil
	}

	// Non-desktop user messages are emitted after turn identity is allocated below.
	// Desktop UI already adds its own messages via handleSend; skip to avoid duplicates.

	b.mu.Lock()
	if b.cancel != nil {
		// Agent is busy — queue the message (mirrors Fyne QueueMessage)
		meta := &data
		if source == "desktop" {
			meta = nil
		}
		b.pendingMsgs.Enqueue(userMsg, false, meta)
		b.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	turnID, _ := b.startDesktopTurnLocked()
	if b.OnStreamEvent != nil && source != "desktop" {
		raw, _ := json.Marshal(map[string]string{"turn_id": turnID, "message_id": fmt.Sprintf("user-%s", turnID), "text": userMsg, "source": source})
		b.OnStreamEvent("user_message", raw)
	}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()

		// Process queued messages (mirrors Fyne line 906-919)
		if pending, ok := b.drainPending(); ok {
			// Notify frontend that a pending message is being consumed
			if b.OnStreamEvent != nil {
				b.OnStreamEvent("pending_consumed", nil)
			}
			if pending.Hidden {
				_ = b.SendHiddenText(pending.Text)
			} else {
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushSystemMessage("Processing queued message...")
				}
				data := tunnel.MessageData{Text: pending.Text}
				src := "desktop"
				if pending.Meta != nil {
					data = *pending.Meta
					src = "mobile"
				}
				_ = b.sendMessageData(data, src, "")
			}
		}
	}()

	if b.agent == nil {
		if err := b.InitAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	// Ensure we have a session (mirrors Fyne bridge.ensureSession)
	if err := b.ensureSession(); err != nil {
		return fmt.Errorf("ensure session: %w", err)
	}
	// Rebind the per-session projection broker before every run so subsequent
	// turns cannot inherit a stale broker callback/session binding.
	b.bindTunnelProjectionSession()
	b.appendLiveUserMessage(userMsg)

	// Notify mobile client: user message + busy status
	// Tunnel is the desktop↔mobile channel; only push desktop-originated messages outbound.
	// Mobile-originated messages are already in HandleTunnelUserMessage (inbound).
	// IM messages are inbound through a separate channel, not routed through tunnel.
	if broker := b.currentTunnelBroker(); broker != nil && source == "desktop" {
		if strings.TrimSpace(data.MessageID) == "" {
			data.MessageID = broker.NextMessageID()
		}
		broker.PushUserMessageData(data)
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}

	// Echo user message to IM channels so other IM surfaces can see it.
	// For IM-originated messages, exclude the source adapter to prevent self-echo.
	// For desktop/mobile messages, broadcast to all IM adapters.
	if b.Emitter != nil {
		if source == "im" && excludeAdapter != "" {
			b.Emitter.EmitUserTextExcept(userMsg, excludeAdapter)
		} else if source != "im" {
			b.Emitter.EmitUserText(userMsg)
		}
	}

	err := b.agent.RunStream(ctx, userMsg, func(ev provider.StreamEvent) {
		if b.OnStreamEvent == nil {
			return
		}
		b.emit(ev)
	})
	if err != nil {
		b.appendLiveError(err.Error())
	}
	// Save session after each message (mirrors Fyne bridge)
	b.saveSession()

	// Mirror TUI handleDoneMsg: always push idle + clear activity
	// when the entire agent run finishes (success or error).
	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, false)
		broker.PushStatus(tunnel.StatusIdle, "")
		broker.PushActivity("")
	}

	// Signal run complete (the entire agent run, not just one turn)
	if b.metricCollector != nil {
		b.metricCollector.Flush()
	}
	b.emitTurnDigest()
	b.resetTunnelRoundState()
	if b.OnStreamEvent != nil {
		b.mu.Lock()
		turnID := b.desktopTurnID
		assistantID := b.desktopAssistantID
		b.mu.Unlock()
		raw, _ := json.Marshal(map[string]interface{}{"turn_id": turnID, "message_id": assistantID, "error": ""})
		if err != nil {
			raw, _ = json.Marshal(map[string]interface{}{"turn_id": turnID, "message_id": assistantID, "error": err.Error()})
		}
		b.OnStreamEvent("run_done", raw)
	}

	return err
}

// Cancel stops the current agent run.
// Mirrors Fyne AgentBridge.Cancel exactly.
func (b *ChatBridge) Cancel() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.cancelled = true
	b.mu.Unlock()

	if b.interactions != nil {
		b.interactions.CancelAll()
	}

	// Notify frontend to close dialogs
	if b.OnStreamEvent != nil {
		b.OnStreamEvent("approval:cancel", json.RawMessage(`{}`))
		b.OnStreamEvent("ask_user:cancel", json.RawMessage(`{}`))
	}

	// Push cancelled status to mobile (mirrors Fyne line 1108-1115)
	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, false)
		broker.PushStatus(tunnel.StatusIdle, "cancelled")
		broker.PushActivity("")
	}
}

// ClearCurrentSession resets the current session so next chat creates a fresh one.
func (b *ChatBridge) ClearCurrentSession() {
	// Clean up ephemeral empty session before switching.
	b.cleanupEphemeralSession()

	state := agentruntime.ClearSession()
	b.ResetAgent()
	b.mu.Lock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = nil
	b.metricEvents = nil
	b.pendingDigests = nil
	if b.tunnelHost != nil {
		b.tunnelHost.ResetStreamState()
	}
	b.mu.Unlock()
	b.bindSessionIntegrations(nil)
}

// cleanupEphemeralSession deletes the current session if it was marked
// ephemeral (auto-created because the latest session was locked) and
// has no user messages. Also releases the session lock.
func (b *ChatBridge) cleanupEphemeralSession() {
	b.mu.Lock()
	ses := b.currentSes
	ephemeral := b.sessionEphemeral
	store := b.sessionStore
	b.mu.Unlock()

	if ephemeral && ses != nil && store != nil {
		_ = agentruntime.DeleteSessionIfEmpty(store, ses)
	}
	// Release lock.
	if b.sessionLock != nil {
		b.sessionLock.Release()
		b.sessionLock = nil
	}
	b.sessionEphemeral = false
}

func (b *ChatBridge) setSessionState(state agentruntime.SessionState) {
	b.mu.Lock()
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = nil
	b.metricEvents = nil
	b.pendingDigests = nil
	if b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	if b.tunnelHost != nil {
		b.tunnelHost.ResetStreamState()
	}
	ses := b.currentSes
	b.mu.Unlock()
	b.bindSessionIntegrations(ses)
}

func (b *ChatBridge) bindSessionIntegrations(ses *session.Session) {
	b.mu.Lock()
	store := b.sessionStore
	tunnelHost := b.tunnelHost
	onSessionChanged := b.OnSessionChanged
	b.mu.Unlock()

	if tunnelHost != nil && ses != nil && store != nil {
		tunnelHost.BindSession(ses, store)
	}
	if onSessionChanged != nil {
		onSessionChanged()
	}
}

// LoadSession loads an existing session by ID.
func (b *ChatBridge) LoadSession(id string) error {
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("init session store: %w", err)
		}
		b.sessionStore = store
	}
	state, err := agentruntime.LoadSession(b.sessionStore, id)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	b.ResetAgent()
	b.setSessionState(state)
	if err := b.InitAgent(context.Background()); err != nil {
		return fmt.Errorf("init agent for session load: %w", err)
	}
	agentruntime.RestoreSessionIntoAgent(b.agent, state.Session)
	return nil
}

// ensureSession creates a new session if none exists (mirrors Fyne bridge).
// EnsureSession creates a new session if one doesn't already exist.
// Called on startup and before sending messages.
func (b *ChatBridge) ensureSession() error {
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("create session store: %w", err)
		}
		b.sessionStore = store
	}
	vendor, endpoint, model := "", "", ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(b.sessionStore, b.currentSes, vendor, endpoint, model, b.workingDir)
	if err != nil {
		return fmt.Errorf("save new session: %w", err)
	}
	if created {
		b.setSessionState(state)
	}
	return nil
}

// saveSession persists the current session (mirrors Fyne bridge).
func (b *ChatBridge) saveSession() {
	b.mu.Lock()
	ses := b.currentSes
	store := b.sessionStore
	agent := b.agent
	digests := b.pendingDigests
	b.pendingDigests = nil
	b.mu.Unlock()
	if agent != nil {
		_ = agentruntime.SaveAgentSessionSnapshotWithExtra(store, ses, agent, digests)
		return
	}
	msgs := ses.Messages
	if len(digests) > 0 {
		msgs = append(msgs, digests...)
	}
	_ = agentruntime.SaveSessionMessages(store, ses, msgs)
}

func (b *ChatBridge) StartNewSession() (string, error) {
	b.ClearCurrentSession()
	if err := b.ensureSession(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes == nil {
		return "", fmt.Errorf("new session was not initialized")
	}
	return b.currentSes.ID, nil
}

// CurrentSessionID returns the current session ID.
func (b *ChatBridge) CurrentSessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes == nil {
		return ""
	}
	return b.currentSes.ID
}

// EnsureSession creates a default session if none exists (mirrors Fyne's ensureSession).
// On first call with no current session, tries to auto-load the most recent
// workspace session. If that session is locked by another instance, creates
// a new ephemeral session (auto-deleted if empty on close/switch).
func (b *ChatBridge) EnsureSession() {
	// Ensure session store is initialized
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return
		}
		b.sessionStore = store
	}
	if b.currentSes != nil {
		return // already have a session
	}

	// Try to auto-load the most recent workspace session.
	if latest, err := b.sessionStore.LatestForWorkspace(b.workingDir); err == nil && latest != nil {
		storeDir, _ := session.DefaultDir()
		lock, lockErr := session.TryAcquireSessionLock(storeDir, latest.ID)
		if lockErr == nil && lock != nil && lock.Acquired() {
			// Got the lock — load this session.
			b.sessionLock = lock
			b.sessionEphemeral = false
			ses, loadErr := b.sessionStore.Load(latest.ID)
			if loadErr == nil && ses != nil {
				b.setSessionState(agentruntime.AdoptSession(ses))
				if b.OnSessionChanged != nil {
					b.OnSessionChanged()
				}
				return
			}
		}
	}

	// Fallback: create a new ephemeral session.
	b.sessionEphemeral = true
	vendor, endpoint, model := "", "", ""
	if b.cfg != nil {
		vendor = b.cfg.Vendor
		endpoint = b.cfg.Endpoint
		model = b.cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(b.sessionStore, b.currentSes, vendor, endpoint, model, b.workingDir)
	if err != nil || !created {
		return
	}
	b.setSessionState(state)

	// Acquire lock on the new session too.
	storeDir, _ := session.DefaultDir()
	if b.currentSes != nil {
		lock, _ := session.TryAcquireSessionLock(storeDir, b.currentSes.ID)
		if lock != nil && lock.Acquired() {
			b.sessionLock = lock
		}
	}
}

// InitAgent sets up provider, tools, and agent — full parity with Fyne bridge.
// Called on startup or before the first message if not yet initialized.
func (b *ChatBridge) InitAgent(_ ...context.Context) error {
	// Permission policy (auto mode)
	mode := agentruntime.InteractivePermissionModeWithDefault(b.cfg, false, "auto")
	b.permissionMode = mode
	policy := agentruntime.BuildInteractivePermissionPolicy(b.cfg, b.workingDir, false)

	// Impersonation
	if b.cfg.Impersonation.Preset != "" && b.cfg.Impersonation.Preset != "none" {
		if preset := provider.FindPresetByID(b.cfg.Impersonation.Preset); preset != nil {
			provider.SetActiveImpersonation(preset, b.cfg.Impersonation.CustomVersion, b.cfg.Impersonation.CustomHeaders)
		}
	}

	resolved, p, err := agentruntime.ResolveCurrentSelection(b.cfg)
	if err != nil {
		return fmt.Errorf("resolve provider selection: %w", err)
	}
	b.resolved = resolved

	core, err := agentruntime.BuildInteractiveRuntimeCore(b.cfg, b.workingDir, policy)
	if err != nil {
		return fmt.Errorf("build runtime core: %w", err)
	}
	b.registry = core.Registry

	// Cron tools
	b.cronScheduler = agentruntime.NewWorkspaceCronScheduler(b.workingDir, func(prompt string) {
		log.Printf("[cron] enqueued prompt: %s", prompt)
	})
	agentruntime.RegisterCronTools(b.registry, b.cronScheduler)
	mcpMgr := core.MCPManager
	b.mcpManager = mcpMgr
	// Push MCP server status changes to frontend via stream events
	if mcpMgr != nil {
		mcpMgr.SetOnUpdate(func(servers []plugin.MCPServerInfo) {
			raw, _ := json.Marshal(servers)
			if b.OnStreamEvent != nil {
				b.OnStreamEvent("mcp:status", raw)
			}
		})
	}
	// Start all background services (MCP connections, etc.)
	core.StartBackgroundServices()
	// Close old tunnel host (stops any active share) before setting new one
	if b.tunnelHost != nil {
		b.tunnelHost.Close()
	}
	// Set unified tunnel host for mobile streaming
	b.tunnelHost = core.Tunnel
	if b.currentSes != nil {
		b.bindSessionIntegrations(b.currentSes)
	}
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	commandMgr := core.CommandManager
	saveMemoryTool := core.SaveMemoryTool
	// When save_memory saves, rebuild system prompt so agent sees new memory
	// (mirrors Fyne setupAgent line 710)
	saveMemoryTool.SetAfterSave(func() {
		newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
		b.mu.Lock()
		if b.agent != nil {
			b.agent.UpdateSystemPrompt(newPrompt)
		}
		b.mu.Unlock()
	})

	// ACP client manager (mirrors Fyne setupAgent)
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.acpClientMgr = acpclient.NewClientManager(b.workingDir, policy)
	b.acpClientMgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		return b.RequestApproval(ctx, "", toolName, input)
	})
	// Sub-agent manager
	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}
	// Build sub-agent system prompt builder (shared by SpawnAgentTool and SkillTool)
	subAgentPromptBuilder := func(task, agentType string) string {
		return agentruntime.BuildSubAgentSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:              b.cfg,
			WorkingDir:       b.workingDir,
			Registry:         b.registry,
			CommandMgr:       commandMgr,
			GlobalAutoMem:    autoMem,
			ProjectAutoMem:   projectAutoMem,
			GitStatus:        func() string { return "" },
			RemoteAgentsInfo: func() string { return "" },
		}, task, agentType)
	}

	b.subAgentMgr = agentruntime.NewSubAgentManager(b.cfg.SubAgents, b.registry, p, b.workingDir, b.recordSessionUsage, agentFactory, subAgentPromptBuilder)
	_ = b.registry.Register(agentruntime.NewSkillTool(commandMgr, mcpMgr, p, b.registry, agentFactory, b.workingDir, b.recordSessionUsage, subAgentPromptBuilder))
	agentruntime.RegisterDelegateTool(b.registry, b.acpClientMgr, func() *subagent.Manager { return b.subAgentMgr }, b.workingDir, func() string {
		if b.agent != nil {
			return b.agent.WorkingDir()
		}
		return b.workingDir
	})

	// Forward sub-agent events to frontend
	b.subAgentMgr.SetOnStreamText(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_text", raw)
		agentruntime.PushTunnelSubagentText(b.currentTunnelBroker, agentID, text)
	})
	b.subAgentMgr.SetOnReasoning(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_reasoning", raw)
		agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
	})
	b.subAgentMgr.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
		if displayName == "" {
			pres := tool.DescribeTool(toolName, args)
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{
				"agentID": agentID, "title": b.subagentPanelTitle(agentID), "id": toolID, "name": toolName,
				"displayName": displayName, "arguments": args, "detail": detail,
			})
			b.OnStreamEvent("subagent_tool_call", raw)
		}
		agentruntime.PushTunnelSubagentToolCall(b.currentTunnelBroker, agentID, toolID, toolName, displayName, args, detail)
	})
	b.subAgentMgr.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
		if displayName == "" {
			pres := tool.DescribeTool(toolName, "")
			displayName = pres.DisplayName
			detail = pres.Detail
		}
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]interface{}{
				"agentID": agentID, "title": b.subagentPanelTitle(agentID), "id": toolID, "name": toolName,
				"displayName": displayName, "detail": detail,
				"result": result, "isError": isError,
			})
			b.OnStreamEvent("subagent_tool_result", raw)
		}
		agentruntime.PushTunnelSubagentToolResult(b.currentTunnelBroker, agentID, toolID, toolName, displayName, detail, result, isError)
	})

	// Notify frontend when a sub-agent completes
	b.subAgentMgr.SetOnComplete(func(sa *subagent.SubAgent) {
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]interface{}{
				"agentID": sa.ID,
				"title":   b.subagentPanelTitle(sa.ID),
				"isError": sa.Status == subagent.StatusFailed,
			})
			b.OnStreamEvent("subagent_done", raw)
		}
	})

	// Swarm manager
	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		cloned := b.registry.Clone() // each teammate gets independent tool instances with MCP/plugins
		// Unconditionally remove tools that teammates must never use.
		for _, name := range []string{
			"ask_user", "spawn_agent", "wait_agent", "list_agents",
			"teammate_spawn", "teammate_shutdown", "team_create", "team_delete",
		} {
			cloned.Unregister(name)
		}
		return cloned
	}
	b.swarmMgr = agentruntime.NewSwarmManager(b.cfg.Swarm, p, b.registry, nil, swarmFactory, toolBuilder)
	b.swarmMgr.SetSystemPromptBuilder(func(name, teamName, wd string) string {
		return agentruntime.BuildTeammateSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:              b.cfg,
			WorkingDir:       wd,
			Registry:         b.registry,
			CommandMgr:       commandMgr,
			GlobalAutoMem:    autoMem,
			ProjectAutoMem:   projectAutoMem,
			GitStatus:        func() string { return "" },
			RemoteAgentsInfo: func() string { return "" },
		}, name, teamName)
	})

	b.registry.Register(tool.SendMessageTool{Manager: b.subAgentMgr, SwarmMgr: b.swarmMgr})

	// Forward swarm events to frontend AND mobile tunnel (mirrors Fyne line 605-698)
	b.swarmMgr.SetOnUpdate(func(ev swarm.Event) {
		// Push to frontend
		if b.OnStreamEvent != nil {
			if ev.TeamID != "" && shouldEmitSwarmBoardUpdate(ev.Type) {
				raw, _ := json.Marshal(map[string]string{"teamID": ev.TeamID})
				b.OnStreamEvent("swarm_board_updated", raw)
			}
			switch ev.Type {
			case "teammate_text":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "content": ev.Result,
				})
				b.OnStreamEvent("swarm_text", raw)
			case "teammate_tool_call":
				pres := tool.DescribeTool(ev.CurrentTool, ev.ToolArgs)
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "id": ev.ToolID, "name": ev.CurrentTool,
					"arguments": ev.ToolArgs, "displayName": pres.DisplayName, "detail": pres.Detail,
				})
				b.OnStreamEvent("swarm_tool_call", raw)
			case "teammate_tool_result":
				pres := tool.DescribeTool(ev.CurrentTool, "")
				raw, _ := json.Marshal(map[string]interface{}{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName,
					"teamID": ev.TeamID, "id": ev.ToolID, "name": ev.CurrentTool,
					"displayName": pres.DisplayName, "detail": pres.Detail,
					"result": ev.Result, "isError": ev.IsError,
				})
				b.OnStreamEvent("swarm_tool_result", raw)
			case "teammate_spawned":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName, "teamID": ev.TeamID,
				})
				b.OnStreamEvent("swarm_spawned", raw)
			case "teammate_idle":
				raw, _ := json.Marshal(map[string]string{
					"teammateID": ev.TeammateID, "teammateName": ev.TeammateName, "teamID": ev.TeamID,
					"content": ev.Result,
				})
				b.OnStreamEvent("swarm_idle", raw)
			}
		}

		// Push to mobile tunnel (mirrors Fyne line 648-698)
		if broker := b.currentTunnelBroker(); broker != nil {
			_ = broker
			agentruntime.PushTunnelSwarmEvent(
				b.currentTunnelBroker,
				b.swarmMgr,
				ev,
				func(toolName, args string) string {
					pres := tool.DescribeTool(toolName, args)
					return pres.DisplayName
				},
				func(toolName, args string) string {
					pres := tool.DescribeTool(toolName, args)
					return pres.Detail
				},
			)
		}
	})

	// Create agent — mirror Fyne setupAgent exactly
	systemPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
	maxIter := b.cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 200
	}
	a := agent.NewAgent(p, b.registry, systemPrompt, maxIter)
	core.SetConfigAgent(a)
	core.SetConfigUINotify(func() {
		b.OnConfigProviderChanged()
	})
	a.SetPermissionPolicy(policy)

	// Usage handler — accumulate token usage per session (mirrors Fyne recordSessionUsage)
	a.SetUsageHandler(func(usage provider.TokenUsage) {
		b.recordSessionUsage(usage)
	})

	// Metric collector — async, non-blocking (mirrors Fyne line 715-721)
	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	b.metricCancel = collectorCancel
	b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
		b.recordMetric(ev)
	})
	a.SetMetricHandler(b.metricCollector.Emit)

	// Context window — critical for context compaction (mirrors Fyne line 737-742)
	agentruntime.ApplyResolvedLimitsToAgent(a, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, resolved, a, func(resp relaycatalog.ResolveResponse) {
		b.mu.Lock()
		if b.resolved != nil {
			if resp.ContextWindow > 0 {
				b.resolved.ContextWindow = resp.ContextWindow
			}
			if resp.MaxOutputTokens > 0 {
				b.resolved.MaxTokens = resp.MaxOutputTokens
			}
		}
		b.mu.Unlock()
	})

	// Wire approval handler
	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {
		requestID := ""
		if broker := b.currentTunnelBroker(); broker != nil {
			requestID = broker.NextMessageID()
		}
		return b.RequestApproval(ctx, requestID, toolName, input)
	})

	// Wire ask_user handler
	if askTool, ok := b.registry.Get("ask_user"); ok {
		if aut, ok := askTool.(*tool.AskUserTool); ok {
			aut.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {
				requestID := ""
				if broker := b.currentTunnelBroker(); broker != nil {
					requestID = broker.NextMessageID()
				}
				return b.RequestAskUser(ctx, requestID, req)
			})
		}
	}

	_, _ = agentruntime.ApplyProjectMemoryToAgent(a, b.workingDir)

	b.agent = a

	// Start A2A server for LAN agent-to-agent communication.
	b.startA2A(b.cfg, a, b.registry)

	// Set interruption handler — agent checks for pending messages during compact etc.
	// (mirrors Fyne line 836-839)
	a.SetInterruptionHandler(func() string {
		return b.drainPendingInterrupt()
	})
	b.EnsureSession()               // mirrors Fyne setupAgent line 743
	b.bindTunnelProjectionSession() // record events even before Share (mirrors Fyne line 303)
	return nil
}

func (b *ChatBridge) StartMCPOAuth(ctx context.Context, serverName string, openURL func(string) error) (*MCPOAuthStartResult, error) {
	if b == nil || b.mcpManager == nil {
		return nil, fmt.Errorf("MCP manager not initialized")
	}
	oauthErr := b.mcpManager.PendingOAuth()
	if oauthErr == nil || oauthErr.Handler == nil || oauthErr.ServerName != serverName {
		return nil, fmt.Errorf("MCP server %q is not waiting for OAuth login", serverName)
	}

	handler := oauthErr.Handler
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if handler.SupportsDCR() {
		if err := handler.RegisterClient(startCtx); err != nil {
			debug.Log("mcp-oauth", "desktop_dcr_failed server=%s error=%v, continuing", serverName, err)
		}
	}

	result := &MCPOAuthStartResult{ServerName: serverName}
	if handler.SupportsDeviceFlow() {
		scopes := handler.GetScopes()
		if len(scopes) > 4 {
			scopes = scopes[:4]
		}
		devResp, err := handler.StartDeviceFlow(startCtx, scopes)
		if err == nil {
			result.AuthorizeURL = devResp.VerificationURI
			result.DeviceUserCode = devResp.UserCode
			if openURL != nil {
				if err := openURL(result.AuthorizeURL); err != nil {
					result.OpenError = err.Error()
				}
			}
			return result, nil
		}
		debug.Log("mcp-oauth", "desktop_device_flow_failed server=%s error=%v, falling back", serverName, err)
	}

	authorizeURL, err := handler.StartAuthFlow(startCtx)
	if err != nil {
		return nil, err
	}
	result.AuthorizeURL = authorizeURL
	if openURL != nil {
		if err := openURL(authorizeURL); err != nil {
			result.OpenError = err.Error()
		}
	}
	return result, nil
}

func (b *ChatBridge) CompleteMCPOAuth(ctx context.Context, serverName string) error {
	if b == nil || b.mcpManager == nil {
		return fmt.Errorf("MCP manager not initialized")
	}
	oauthErr := b.mcpManager.PendingOAuth()
	if oauthErr == nil || oauthErr.Handler == nil || oauthErr.ServerName != serverName {
		return fmt.Errorf("MCP server %q is not waiting for OAuth login", serverName)
	}

	handler := oauthErr.Handler
	completeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var tokenRespErr error
	if handler.HasPendingDeviceFlow() {
		tokenResp, err := handler.PollDeviceToken(completeCtx)
		if err != nil {
			tokenRespErr = err
		} else {
			tokenRespErr = handler.SaveToken(tokenResp)
		}
	} else {
		code, err := handler.WaitForCallback(completeCtx)
		if err != nil {
			tokenRespErr = err
		} else {
			tokenResp, err := handler.ExchangeCode(completeCtx, code)
			if err != nil {
				tokenRespErr = err
			} else {
				tokenRespErr = handler.SaveToken(tokenResp)
			}
		}
	}
	if tokenRespErr != nil {
		return tokenRespErr
	}

	handler.ShutdownCallbackServer()
	b.mcpManager.ClearPendingOAuth()
	if !b.mcpManager.Retry(serverName) {
		return fmt.Errorf("MCP server %q not found for reconnect", serverName)
	}
	return nil
}

func (b *ChatBridge) subagentPanelTitle(agentID string) string {
	if b.subAgentMgr == nil {
		return agentID
	}
	snap, ok := b.subAgentMgr.SnapshotByID(agentID)
	if !ok {
		return agentID
	}
	// Name is set from spawn_agent's "description" field (short label)
	switch {
	case strings.TrimSpace(snap.Name) != "":
		return snap.Name
	case strings.TrimSpace(snap.DisplayTask) != "":
		return snap.DisplayTask
	case strings.TrimSpace(snap.Task) != "":
		return snap.Task
	default:
		return agentID
	}
}

func (b *ChatBridge) emit(ev provider.StreamEvent) {
	var eventType string
	var data interface{}
	var semantic agentruntime.DesktopStreamSemantic
	var ok bool

	switch ev.Type {
	case provider.StreamEventToolCallChunk:
		eventType = "tool_call_chunk"
		data = map[string]interface{}{
			"id":   ev.Tool.ID,
			"name": ev.Tool.Name,
		}

	default:
		semantic, ok = agentruntime.HandleDesktopStreamEvent(ev, &b.imRound,
			agentruntime.NewDesktopEmitterAdapter(agentruntime.DesktopEmitterCallbacks{
				TriggerTypingFn: func() {
					if b.Emitter != nil {
						b.Emitter.TriggerTyping()
					}
				},
				EmitToolResultFn: func(toolName, rawArgs, result string, isError bool) {
					if b.Emitter == nil {
						return
					}
					b.Emitter.EmitEvent(im.OutboundEvent{
						Kind: im.OutboundEventToolResult,
						ToolRes: &im.ToolResultInfo{
							ToolName: toolName,
							Args:     rawArgs,
							Result:   result,
							IsError:  isError,
						},
					})
				},
				EmitRoundSummaryFn: func(text string, toolCalls, toolSuccesses, toolFailures int) {
					if b.Emitter != nil {
						b.Emitter.EmitRoundSummary(text, toolCalls, toolSuccesses, toolFailures)
					}
				},
			}),
			nil,
		)
		if !ok {
			return
		}
		b.applySemanticToLiveHistory(semantic)
		switch semantic.Type {
		case provider.StreamEventText:
			eventType = "text"
			turnID, assistantID, seq := b.desktopTurnSnapshot()
			data = map[string]interface{}{"turn_id": turnID, "message_id": assistantID, "seq": seq, "content": semantic.Text}
		case provider.StreamEventToolCallDone:
			eventType = "tool_call_done"
			data = map[string]interface{}{
				"id":          semantic.ToolCall.ID,
				"toolID":      semantic.ToolCall.ID,
				"tool_id":     semantic.ToolCall.ID,
				"name":        semantic.ToolCall.Name,
				"arguments":   semantic.ToolCall.RawArgs,
				"displayName": semantic.ToolCall.DisplayName,
				"detail":      semantic.ToolCall.Detail,
			}
		case provider.StreamEventToolResult:
			eventType = "tool_result"
			data = map[string]interface{}{
				"id":      semantic.ToolResult.ID,
				"toolID":  semantic.ToolResult.ID,
				"tool_id": semantic.ToolResult.ID,
				"name":    semantic.ToolResult.Name,
				"result":  semantic.ToolResult.Preview,
				"isError": semantic.ToolResult.IsError,
			}
		case provider.StreamEventDone:
			eventType = "done"
			b.mu.Lock()
			turnID := b.desktopTurnID
			assistantID := b.desktopAssistantID
			b.mu.Unlock()
			data = map[string]interface{}{"turn_id": turnID, "message_id": assistantID, "usage": semantic.UsageData}
			// Advance assistantID so the next LLM iteration creates a new
			// assistant message instead of appending to the previous one.
			b.mu.Lock()
			b.desktopTextSeq++
			b.desktopAssistantID = fmt.Sprintf("assistant-turn-%d-iter-%d", b.desktopTurnCounter, b.desktopTextSeq)
			b.mu.Unlock()
		case provider.StreamEventError:
			eventType = "error"
			data = map[string]string{"message": semantic.ErrorText}
		case provider.StreamEventReasoning:
			eventType = "reasoning"
			data = map[string]string{"content": semantic.Text}
		default:
			return
		}
	}

	raw, _ := json.Marshal(data)
	if b.OnStreamEvent != nil {
		b.OnStreamEvent(eventType, raw)
	}

	// Push to tunnel via unified TunnelHost
	if b.tunnelHost != nil {
		b.tunnelHost.PushStreamEvent(ev)
	}
}

func (b *ChatBridge) CurrentSessionHistory() []SessionMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) > 0 {
		out := make([]SessionMessage, len(b.liveHistory))
		copy(out, b.liveHistory)
		return out
	}
	if b.currentSes == nil {
		return nil
	}
	return buildSessionHistoryFromMessages(b.currentSes.Messages)
}

func (b *ChatBridge) appendLiveUserMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	b.liveHistory = append(b.liveHistory, SessionMessage{
		ID:      fmt.Sprintf("user-%s", b.desktopTurnID),
		TurnID:  b.desktopTurnID,
		Role:    "user",
		Content: text,
	})
}

func (b *ChatBridge) appendLiveError(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "error",
		Content: text,
	})
}

func (b *ChatBridge) applySemanticToLiveHistory(semantic agentruntime.DesktopStreamSemantic) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = buildSessionHistoryFromMessages(b.currentSes.Messages)
	}
	switch semantic.Type {
	case provider.StreamEventReasoning:
		if semantic.Text == "" {
			return
		}
		if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "reasoning" && b.liveHistory[n-1].Streaming {
			b.liveHistory[n-1].Content += semantic.Text
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:      "reasoning",
			Content:   semantic.Text,
			Streaming: true,
		})
	case provider.StreamEventText:
		b.finalizeLiveReasoningLocked()
		assistantID := b.desktopAssistantID
		turnID := b.desktopTurnID
		if assistantID == "" {
			turnID, assistantID = b.startDesktopTurnLocked()
		}
		for i := len(b.liveHistory) - 1; i >= 0; i-- {
			if b.liveHistory[i].ID == assistantID && b.liveHistory[i].Role == "assistant" {
				b.liveHistory[i].Content += semantic.Text
				b.liveHistory[i].Streaming = true
				return
			}
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			ID:        assistantID,
			TurnID:    turnID,
			Role:      "assistant",
			Content:   semantic.Text,
			Streaming: true,
		})
	case provider.StreamEventToolCallDone:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
		if semantic.ToolCall == nil {
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:        "tool",
			ToolName:    semantic.ToolCall.Name,
			ToolID:      semantic.ToolCall.ID,
			ToolArgs:    semantic.ToolCall.RawArgs,
			ToolDisplay: semantic.ToolCall.DisplayName,
			ToolDetail:  semantic.ToolCall.Detail,
			Streaming:   true,
		})
	case provider.StreamEventToolResult:
		if semantic.ToolResult == nil {
			return
		}
		for i := len(b.liveHistory) - 1; i >= 0; i-- {
			if b.liveHistory[i].Role == "tool" && b.liveHistory[i].ToolID == semantic.ToolResult.ID {
				b.liveHistory[i].Content = semantic.ToolResult.Preview
				b.liveHistory[i].IsError = semantic.ToolResult.IsError
				b.liveHistory[i].Streaming = false
				break
			}
		}
	case provider.StreamEventDone:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
	case provider.StreamEventError:
		b.finalizeLiveReasoningLocked()
		b.finalizeStreamingAssistantLocked()
		if semantic.ErrorText == "" {
			return
		}
		b.liveHistory = append(b.liveHistory, SessionMessage{
			Role:    "error",
			Content: semantic.ErrorText,
		})
	}
}

func (b *ChatBridge) finalizeLiveReasoningLocked() {
	if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "reasoning" && b.liveHistory[n-1].Streaming {
		b.liveHistory[n-1].Streaming = false
		// Notify frontend that reasoning is complete
		if b.OnStreamEvent != nil {
			b.OnStreamEvent("reasoning_done", nil)
		}
	}
}

func (b *ChatBridge) finalizeStreamingAssistantLocked() {
	if n := len(b.liveHistory); n > 0 && b.liveHistory[n-1].Role == "assistant" && b.liveHistory[n-1].Streaming {
		b.liveHistory[n-1].Streaming = false
	}
}

// GetModelInfo returns the current model info for the status bar.
func (b *ChatBridge) GetModelInfo() map[string]interface{} {
	if b.cfg == nil {
		return nil
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return map[string]interface{}{
			"vendor":       b.cfg.Vendor,
			"model":        b.cfg.Model,
			"mode":         b.GetPermissionMode(),
			"contextTotal": 0,
			"effort":       displayReasoningEffort(b.ReasoningEffort()),
		}
	}
	payload := map[string]interface{}{
		"vendor":        b.cfg.Vendor,
		"model":         b.cfg.Model,
		"contextWindow": resolved.ContextWindow,
		"contextTotal":  resolved.ContextWindow,
		"mode":          b.GetPermissionMode(),
		"effort":        displayReasoningEffort(b.ReasoningEffort()),
	}
	for key, value := range b.currentUsagePayload() {
		payload[key] = value
	}
	return payload
}

// ─── Tunnel Broker Integration ──────────────────────────────────────
// Full parity with Fyne AgentBridge tunnel logic.

// AttachTunnelBroker connects the broker for outbound event push to mobile.
// All negotiation (session_info, replay, status, announce) is handled by
// TunnelHost.PrepareOnlineShare — the canonical share bootstrap.
func (b *ChatBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	var (
		currentSes *session.Session
		working    bool
		cfg        *config.Config
	)
	b.mu.Lock()
	currentSes = b.currentSes
	working = b.cancel != nil
	cfg = b.cfg
	b.mu.Unlock()

	if broker == nil {
		return
	}

	// Set snapshot provider for the "no replay events" fallback.
	broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		snapshot := tunnel.BrokerSnapshot{}
		if working && cfg != nil {
			status := b.CurrentTunnelStatus()
			snapshot.Status = status
			activity := b.CurrentTunnelActivity()
			if activity != "" {
				snapshot.Activity = tunnel.ActivityData{Activity: activity}
			}
		}
		return snapshot
	})

	// Cache session info for PrepareOnlineShare (workspace, model, provider).
	// Must be set unconditionally, not gated on "working" — workspace info
	// is needed even when no agent run is active.
	if cfg != nil {
		resolved, _ := cfg.ResolveActiveEndpoint()
		model := ""
		vendorName := ""
		if resolved != nil {
			model = resolved.Model
			vendorName = resolved.VendorName
		}
		if b.tunnelHost != nil {
			b.tunnelHost.SetSessionInfo(tunnel.SessionInfoData{
				Title:     currentSes.Title,
				Workspace: b.workingDir,
				Model:     model,
				Provider:  vendorName,
				Mode:      cfg.DefaultMode,
				Language:  cfg.Language,
			})
		}
	}

	// Delegate ALL negotiation to TunnelHost.PrepareOnlineShare:
	// SendSessionInfo, BindSession, SetReplayProvider, SetAuthorityEpoch,
	// Replay/Snapshot, AnnounceActiveSession.
	if b.tunnelHost != nil {
		b.tunnelHost.AttachOnlineBroker(broker)
		if currentSes != nil {
			b.tunnelHost.BindSession(currentSes, b.sessionStore)
		}
		b.tunnelHost.PrepareOnlineShare(broker)
	}
}

func (b *ChatBridge) DetachTunnelBroker() {
	if b.tunnelHost != nil {
		b.tunnelHost.DetachOnlineBroker()
	}
}

func (b *ChatBridge) currentTunnelBroker() *tunnel.Broker {
	if b.tunnelHost != nil {
		if pb := b.tunnelHost.ProjectionBroker(); pb != nil {
			return pb
		}
	}
	return nil
}

func (b *ChatBridge) currentShareTunnelBroker() *tunnel.Broker {
	return b.currentTunnelBroker()
}

func (b *ChatBridge) bindTunnelProjectionSession() {
	b.mu.Lock()
	currentSes := b.currentSes
	b.mu.Unlock()
	b.bindSessionIntegrations(currentSes)
}

func (b *ChatBridge) CurrentTunnelStatus() tunnel.StatusData {
	if b.cancel != nil {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

func (b *ChatBridge) CurrentTunnelActivity() string {
	switch {
	case b.interactions != nil && b.interactions.ApprovalCount() > 0:
		return "approval"
	case b.interactions != nil && b.interactions.AskUserCount() > 0:
		return "ask_user"
	case b.cancel != nil:
		return "processing"
	default:
		return ""
	}
}

// TunnelHost handles all message stream state internally.
// These methods are kept as no-op stubs for any remaining callers.

func (b *ChatBridge) ensureTunnelMsgID(broker *tunnel.Broker) string {
	return ""
}

func (b *ChatBridge) tunnelReasoningMsgID(broker *tunnel.Broker) string {
	return ""
}

func (b *ChatBridge) markTunnelMainStreamActive() {}

func (b *ChatBridge) flushTunnelTextStream(broker *tunnel.Broker, force bool) {}

func (b *ChatBridge) resetTunnelRoundState() {}

func (b *ChatBridge) currentSessionTunnelAuthorityEpoch() uint64 {
	if b.tunnelHost != nil {
		return b.tunnelHost.AuthorityEpoch()
	}
	return 1
}

func (b *ChatBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	if b.tunnelHost != nil {
		return b.tunnelHost.TunnelEvents()
	}
	return nil
}

func (b *ChatBridge) pushTunnelSessionInfo(broker *tunnel.Broker) {
	b.mu.Lock()
	cfg := b.cfg
	ses := b.currentSes
	b.mu.Unlock()
	if broker == nil || cfg == nil || ses == nil {
		return
	}
	resolved, _ := cfg.ResolveActiveEndpoint()
	model := ""
	vendorName := ""
	if resolved != nil {
		model = resolved.Model
		vendorName = resolved.VendorName
	}
	broker.SendSessionInfo(tunnel.SessionInfoData{
		Title:     ses.Title,
		Workspace: b.workingDir,
		Model:     model,
		Provider:  vendorName,
		Mode:      cfg.DefaultMode,
	})
}

func (b *ChatBridge) pushTunnelApprovalResult(id, decision string) {
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), id, decision, agentruntime.TunnelStateUpdate{})
}

func (b *ChatBridge) pushTunnelAskUserResponse(id string, response tool.AskUserResponse) {
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), id, response, agentruntime.TunnelStateUpdate{})
}

func (b *ChatBridge) nextTunnelRequestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

func (b *ChatBridge) ResetCurrentSessionTunnelLedger() {
	if b.tunnelHost == nil {
		return
	}
	store := b.tunnelHost.ProjectionStore()
	b.mu.Lock()
	ses := b.currentSes
	b.mu.Unlock()
	if ses == nil || store == nil {
		return
	}
	_ = store.DeleteSession(ses.ID)
}

func (b *ChatBridge) CurrentTunnelHistory() []tunnel.HistoryEntry {
	// TODO: implement when HistoryEntry fields are needed
	return nil
}

// RequestApproval blocks until the user (desktop or mobile) responds to an
// approval request.  It stores a pending channel, pushes the request to a
// connected tunnel broker, and emits an event so the Wails frontend can show
// an approval dialog.
func (b *ChatBridge) RequestApproval(ctx context.Context, requestID, toolName, input string) permission.Decision {
	req := agentruntime.ApprovalRequest{ID: requestID, ToolName: toolName, Input: input}

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelApprovalRequest(broker, requestID, toolName, input, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	// Emit to Wails frontend
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]string{
			"requestID": requestID,
			"toolName":  toolName,
			"input":     input,
		})
		b.OnStreamEvent("approval:request", raw)
	}

	return b.interactions.AwaitApproval(context.WithoutCancel(ctx), req)
}

// RequestAskUser blocks until the user (desktop or mobile) responds to a
// structured questionnaire.  It mirrors the Fyne handleAskUser flow.
func (b *ChatBridge) RequestAskUser(ctx context.Context, requestID string, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: tool.AskUserStatusSubmitted}, nil
	}
	request := agentruntime.AskUserRequest{ID: requestID, Request: req}

	// Push to mobile via tunnel
	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelAskUserRequest(broker, requestID, req, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	// Emit to Wails frontend
	if b.OnStreamEvent != nil {
		payload := map[string]interface{}{
			"requestID": requestID,
			"title":     req.Title,
			"questions": req.Questions,
		}
		raw, _ := json.Marshal(payload)
		b.OnStreamEvent("ask_user:request", raw)
	}

	return b.interactions.AwaitAskUser(context.WithoutCancel(ctx), request)
}

// RespondApproval delivers a desktop-originated approval decision to the
// waiting channel.  decision is "allow", "deny", or "always_allow".
func (b *ChatBridge) RespondApproval(requestID, decision string) {
	d := agentruntime.ApprovalDecisionFromTunnel(decision)
	req, ok := b.interactions.ResolveApproval(requestID, d)
	if !ok {
		return
	}

	// Always-allow: persist override on the agent's permission policy
	if (decision == "always_allow" || decision == "always") && b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			p.SetOverride(req.ToolName, permission.Allow)
		}
	}

	// Push result to mobile
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), requestID, decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

}

func (b *ChatBridge) PendingApprovalRequest() (string, string, bool) {
	req, ok := b.interactions.FirstPendingApproval()
	if !ok {
		return "", "", false
	}
	return req.ID, req.ToolName, true
}

// RespondAskUser delivers a desktop-originated ask_user response to the
// waiting channel.
func (b *ChatBridge) RespondAskUser(requestID string, response tool.AskUserResponse) {
	if _, ok := b.interactions.ResolveAskUser(requestID, response); !ok {
		return
	}

	// Push response to mobile
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), requestID, response, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

}

func (b *ChatBridge) PendingAskUserRequest() (string, tool.AskUserRequest, bool) {
	req, ok := b.interactions.FirstPendingAskUser()
	if !ok {
		return "", tool.AskUserRequest{}, false
	}
	return req.ID, req.Request, true
}

// HandleMobileApprovalResponse processes an approval response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileApprovalResponse(data tunnel.ApprovalResponseData) {
	decision := agentruntime.ResolveTunnelApproval(data.Decision, "", nil)
	req, ok := b.interactions.ResolveApproval(data.ID, decision)
	if !ok {
		return
	}
	agentruntime.ResolveTunnelApproval(data.Decision, req.ToolName, func(toolName string) {
		if b.agent != nil {
			if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
				p.SetOverride(toolName, permission.Allow)
			}
		}
	})

	// Push result to mobile (for relay persistence)
	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), data.ID, data.Decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// HandleMobileAskUserResponse processes an ask_user response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileAskUserResponse(data tunnel.AskUserResponseData, _ tool.AskUserRequest) {
	// Retrieve the original request from interactions broker (not the empty param)
	req, found := b.interactions.PendingAskUser(data.ID)
	if !found {
		return
	}
	response := agentruntime.BuildAskUserResponseFromTunnel(req.Request, data.Status, data.Answers)
	if _, ok := b.interactions.ResolveAskUser(data.ID, response); !ok {
		return
	}

	// Push response to mobile (for relay persistence)
	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), data.ID, response, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// CurrentAskUserRequest returns the pending ask_user request for the given ID,
// or nil if none exists.  Used by HandleMobileAskUserResponse to reconstruct
// the full response with completion metadata.
func (b *ChatBridge) CurrentAskUserRequest(requestID string) tool.AskUserRequest {
	// We don't store the request separately — but HandleMobileAskUserResponse
	// takes it as a parameter from app.go which stores the current ask state.
	return tool.AskUserRequest{}
}

// Messages returns the current conversation messages for snapshot/tunnel use.
// When agent is nil (e.g. after loading a historical session but before
// sending any message), falls back to the session's persisted messages.
func (b *ChatBridge) Messages() []provider.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.agent != nil {
		return b.agent.Messages()
	}
	if b.currentSes != nil {
		return b.currentSes.Messages
	}
	return nil
}

// SetApprovalOverride persists a tool-level permission override.
func (b *ChatBridge) SetApprovalOverride(toolName string) {
	if b.agent != nil {
		if p, ok := b.agent.PermissionPolicy().(*permission.ConfigPolicy); ok {
			p.SetOverride(toolName, permission.Allow)
		}
	}
}

// ─── System Prompt ───────────────────────────────────────────────────

// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly.
// buildWailsSystemPrompt builds the system prompt for the agent.
// Mirrors Fyne buildSystemPrompt exactly — includes auto-memory content.
func buildWailsSystemPrompt(cfg *config.Config, workingDir string, mode permission.PermissionMode, globalAutoMem, projectAutoMem *memory.AutoMemory, commandMgr *commands.Manager) string {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return agentruntime.BuildInteractiveSystemPrompt(cfg, workingDir, mode, nil, commandMgr, globalAutoMem, projectAutoMem, "", "")
}

// ─── Session Usage Tracking ──────────────────────────────────────────

// recordSessionUsage accumulates token usage into the session.
// Mirrors Fyne AgentBridge.recordSessionUsage and TUI Model.recordSessionUsage exactly.
func (b *ChatBridge) recordSessionUsage(usage provider.TokenUsage) {
	b.mu.Lock()
	if b.currentSes == nil || b.sessionStore == nil {
		b.mu.Unlock()
		return
	}
	ses := b.currentSes
	ses.TokenUsage = ses.TokenUsage.Add(usage)
	ses.AddUsageForEndpoint(ses.Vendor, ses.Endpoint, usage)
	ses.UpdatedAt = time.Now()
	store := b.sessionStore
	turnIdx := b.usageTurnIndex
	entry := session.UsageEntry{
		Timestamp: time.Now(),
		TurnIndex: turnIdx,
		Model:     ses.Model,
		Vendor:    ses.Vendor,
		Endpoint:  ses.Endpoint,
		Usage:     usage,
	}
	b.mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetaToDisk(ses)
		_ = jsonlStore.AppendUsageEntry(ses, entry)
	} else {
		_ = store.Save(ses)
	}

	// Notify frontend of updated usage
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(b.currentUsagePayload())
		b.OnStreamEvent("usage_update", raw)
	}
}

func (b *ChatBridge) currentUsagePayload() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	payload := map[string]interface{}{
		"inputTokens":      0,
		"outputTokens":     0,
		"cacheRead":        0,
		"cacheWrite":       0,
		"cacheHit":         0,
		"contextUsed":      0,
		"contextTotal":     0,
		"usagePercent":     0,
		"remainingPercent": 0,
	}
	if b.currentSes != nil {
		usage := b.currentSes.UsageForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint)
		payload["inputTokens"] = usage.DisplayInputTokens()
		payload["outputTokens"] = usage.OutputTokens
		payload["cacheRead"] = usage.CacheRead
		payload["cacheWrite"] = usage.CacheWrite
		payload["cacheHit"] = usage.CacheHitPercent()
	}
	if b.agent != nil {
		cm := b.agent.ContextManager()
		display, ok := uiusage.BuildContextDisplay(cm.TokenCount(), cm.ContextWindow(), cm.AutoCompactThreshold())
		if ok {
			payload["contextUsed"] = display.UsedTokens
			payload["contextTotal"] = display.MaxTokens
			payload["usagePercent"] = display.UsagePercent
			payload["remainingPercent"] = display.RemainingPercent
		}
	}
	return payload
}

// ─── Metrics ──────────────────────────────────────────────────────────

// recordMetric stores a metric event for turn digest generation.
func (b *ChatBridge) recordMetric(ev interface{}) {
	me, ok := ev.(metrics.MetricEvent)
	if !ok {
		return
	}
	b.mu.Lock()
	me.TurnIndex = b.usageTurnIndex
	if b.currentSes != nil {
		me.Model = b.currentSes.Model
		me.Vendor = b.currentSes.Vendor
		me.Endpoint = b.currentSes.Endpoint
		b.currentSes.Metrics = append(b.currentSes.Metrics, me)
		b.currentSes.AppendMetricForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint, me)
	}
	b.metricEvents = append(b.metricEvents, me)
	b.mu.Unlock()
}

func (b *ChatBridge) emitTurnDigest() {
	b.mu.Lock()
	turnIndex := b.usageTurnIndex
	if turnIndex <= 0 || turnIndex <= b.lastMetricDigestTurn {
		b.mu.Unlock()
		return
	}
	turn, ok := metrics.TurnSummaryForIndex(b.metricEvents, turnIndex)
	if !ok {
		b.mu.Unlock()
		return
	}
	lang := "en"
	if b.cfg != nil && b.cfg.Language == "zh-CN" {
		lang = "zh-CN"
	}
	text := metrics.FormatTurnDigest(lang, turn)
	b.lastMetricDigestTurn = turnIndex

	// Persist to liveHistory so CurrentSessionHistory includes it.
	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "system",
		Content: text,
	})
	// Stage digest for the next saveSession() — do NOT write to
	// currentSes.Messages directly, as saveSession() replaces them
	// with agent.Messages().
	digestMsg := provider.Message{Role: "system", Content: []provider.ContentBlock{provider.TextBlock(text)}}
	b.pendingDigests = append(b.pendingDigests, digestMsg)
	b.mu.Unlock()

	// Push to frontend via event stream.
	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]string{
			"type": "system",
			"text": text,
		})
		b.OnStreamEvent("system", raw)
	}
}

// ─── Sub-agent tunnel helpers ────────────────────────────────────────

func tunnelSubagentTextID(agentID string) string {
	return fmt.Sprintf("sa-%s", agentID)
}

func tunnelSubagentReasoningID(agentID string) string {
	return fmt.Sprintf("sa-%s-reasoning", agentID)
}

func (b *ChatBridge) markTunnelSubagentSpawned(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spawnedSet == nil {
		b.spawnedSet = make(map[string]bool)
	}
	if b.spawnedSet[id] {
		return false
	}
	b.spawnedSet[id] = true
	return true
}

func (b *ChatBridge) pushTunnelSubagentEvent(sa *subagent.SubAgent) {
	agentruntime.PushTunnelSubagentEvent(b.currentTunnelBroker, b.markTunnelSubagentSpawned, sa)
}

// ─── Permission Mode ──────────────────────────────────────────────────

// GetPermissionMode returns the current permission mode string.
func (b *ChatBridge) GetPermissionMode() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.permissionMode.String()
}

// SetPermissionMode updates the agent permission mode at runtime.
// Mirrors Fyne AgentBridge.SetPermissionMode exactly.
func (b *ChatBridge) SetPermissionMode(modeStr string) {
	mode := permission.ParsePermissionMode(modeStr)
	b.mu.Lock()
	b.permissionMode = mode
	agent := b.agent
	cfg := b.cfg
	b.mu.Unlock()
	if agent != nil {
		policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode)
		agent.SetPermissionPolicy(policy)
		// Update system prompt to include current mode, overriding any stale
		// autopilot continue instructions that may still be in context.
		b.refreshSystemPrompt()
	}
	// Persist to config file (mirrors TUI/Fyne SaveDefaultModePreference)
	if cfg != nil {
		_ = cfg.SaveDefaultModePreference(modeStr)
	}
}

// ─── Pending Messages ────────────────────────────────────────────────

// QueueMessage stores a user message to be sent after the current agent turn.
func (b *ChatBridge) QueueMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, false, nil)
}

// QueueHiddenMessage stores a hidden message (mirrors Fyne).
func (b *ChatBridge) QueueHiddenMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, true, nil)
}

func (b *ChatBridge) drainPending() (agentruntime.PendingMessage[*tunnel.MessageData], bool) {
	return b.pendingMsgs.Consume()
}

func (b *ChatBridge) drainPendingInterrupt() string {
	pending, ok := b.drainPending()
	if !ok {
		return ""
	}
	if b.OnStreamEvent != nil {
		b.OnStreamEvent("pending_consumed", nil)
	}
	if !pending.Hidden {
		// Persist visible user message
		b.mu.Lock()
		if b.currentSes != nil {
			msg := provider.Message{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(pending.Text)}}
			b.currentSes.Messages = append(b.currentSes.Messages, msg)
			b.currentSes.UpdatedAt = time.Now()
			if b.sessionStore != nil {
				_ = b.sessionStore.Save(b.currentSes)
			}
		}
		b.mu.Unlock()
	}
	return strings.TrimSpace(pending.Text)
}

// SendHiddenText sends a hidden message to the agent without UI display.
func (b *ChatBridge) SendHiddenText(text string) error {
	b.mu.Lock()
	if b.cancel != nil {
		b.pendingMsgs.Enqueue(text, true, nil)
		b.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()
	}()

	if b.agent == nil {
		if err := b.InitAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	return b.agent.RunStream(ctx, text, func(ev provider.StreamEvent) {
		b.emit(ev)
	})
}

// ─── Agent Lifecycle ──────────────────────────────────────────────────

// Close cleans up all resources (mirrors Fyne AgentBridge.Close).
// startA2A starts the A2A server, registers this instance, and wires the
// remote tool so the agent can discover and delegate to other ggcode instances.
func (b *ChatBridge) startA2A(cfg *config.Config, ag *agent.Agent, reg *tool.Registry) {
	// Stop any existing A2A server from a previous setupAgent call.
	b.stopA2A()

	if cfg.A2A.Disabled {
		return
	}

	a2aReg, err := a2a.NewRegistry()
	if err != nil {
		log.Printf("[a2a] failed to create registry: %v", err)
		return
	}

	if cfg.A2A.IsLANDiscovery() {
		a2aReg.EnableLANDiscovery()
	}

	handler := a2a.NewTaskHandler(b.workingDir, ag, reg,
		a2a.WithMaxTasks(cfg.A2A.MaxTasks),
		a2a.WithTimeout(parseA2ATimeout(cfg.A2A.TaskTimeout)),
	)

	srv := a2a.NewServer(a2a.ServerConfig{
		Host:    cfg.A2A.Host,
		Port:    cfg.A2A.Port,
		APIKey:  cfg.A2A.EffectiveAPIKey(),
		APIKeys: cfg.A2A.Auth.APIKeys,
	}, handler)

	if err := srv.Start(); err != nil {
		log.Printf("[a2a] failed to start server: %v", err)
		return
	}

	// Register this instance
	instance := a2a.InstanceInfo{
		ID:           a2a.GenerateInstanceID(),
		PID:          os.Getpid(),
		Workspace:    b.workingDir,
		StartedAt:    time.Now().Format(time.RFC3339),
		Endpoint:     srv.Endpoint(),
		AgentCardURL: srv.Endpoint() + "/.well-known/agent.json",
		Status:       "ready",
	}
	if err := a2aReg.Register(instance); err != nil {
		log.Printf("[a2a] failed to register: %v", err)
		srv.Stop()
		return
	}

	// Register remote tool for agent-to-agent discovery
	apiKey := cfg.A2A.EffectiveAPIKey()
	remoteTool := a2a.NewRemoteTool(a2aReg, apiKey)
	_ = reg.Register(remoteTool)

	// MCP bridge tools for external clients
	bridgeClient := a2a.NewClient(srv.Endpoint(), apiKey)
	for _, t := range a2a.MCPBridgeTools(bridgeClient) {
		_ = reg.Register(t)
	}

	// Background cache refresh
	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	safego.Go("desktop.a2a-cache-refresh", func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				remoteTool.RefreshCache()
			case <-refreshCtx.Done():
				return
			}
		}
	})

	b.a2aServer = srv
	b.a2aRegistry = a2aReg
	b.a2aRemoteTool = remoteTool
	b.a2aRefreshCancel = refreshCancel

	// Mount lanchat handlers on the A2A server mux.
	chatStore := lanchat.NewStore(filepath.Join(config.HomeDir(), "lanchat"))
	b.lanchatHub = lanchat.NewHub(
		a2aReg.SelfID(),
		"gui",
		srv.Endpoint(),
		cfg.A2A.EffectiveAPIKey(),
		chatStore,
	)
	b.lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
	lanchat.MountHandlers(srv.Mux(), b.lanchatHub)
	// Sync peers from A2A registry
	safego.Go("desktop.a2a-peer-sync", func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			if b.a2aRegistry == nil {
				return
			}
			instances := b.a2aRegistry.CachedInstances()
			if instances == nil {
				continue // cache not populated yet
			}
			peers := make([]lanchat.Participant, 0, len(instances))
			for _, inst := range instances {
				peers = append(peers, lanchat.Participant{
					NodeID:   inst.ID,
					Mode:     "gui",
					Endpoint: inst.Endpoint,
					Online:   true,
				})
			}
			b.lanchatHub.UpdatePeers(peers)
		}
	})

	log.Printf("[a2a] server started at %s (lan_discovery=%v)", srv.Endpoint(), cfg.A2A.IsLANDiscovery())
}

// stopA2A shuts down the A2A server and cleans up.
func (b *ChatBridge) stopA2A() {
	if b.a2aRefreshCancel != nil {
		b.a2aRefreshCancel()
		b.a2aRefreshCancel = nil
	}
	if b.a2aRegistry != nil {
		_ = b.a2aRegistry.Unregister()
		b.a2aRegistry = nil
	}
	if b.a2aServer != nil {
		b.a2aServer.Stop()
		b.a2aServer = nil
	}
	// Unregister A2A tools so they don't reference a stopped server/registry.
	if b.a2aRemoteTool != nil {
		b.registry.Unregister(b.a2aRemoteTool.Name())
		b.a2aRemoteTool = nil
	}
	// Unregister MCP bridge tools so they don't reference a stopped server/registry.
	for _, name := range []string{"a2a_discover", "a2a_send_task", "a2a_get_task", "a2a_list_tasks", "a2a_cancel_task"} {
		b.registry.Unregister(name)
	}
}

func parseA2ATimeout(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

func (b *ChatBridge) Close() {
	// Clean up ephemeral empty session before shutting down.
	b.cleanupEphemeralSession()

	// Stop A2A server
	b.stopA2A()

	b.mu.Lock()
	if b.metricCancel != nil {
		b.metricCancel()
	}
	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.mu.Unlock()
}

// IsWorking returns true if the agent is currently running.
func (b *ChatBridge) IsWorking() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancel != nil
}

// Elapsed returns time since the current agent run started.
func (b *ChatBridge) Elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel == nil {
		return 0
	}
	return time.Since(b.startTime)
}

// ContextWindow returns the current context window size.
func (b *ChatBridge) ContextWindow() int {
	b.mu.Lock()
	agent := b.agent
	resolved := b.resolved
	b.mu.Unlock()
	if agent != nil {
		return agent.ContextManager().ContextWindow()
	}
	if resolved != nil {
		return resolved.ContextWindow
	}
	return 0
}

// TokenCount returns the current token usage.
func (b *ChatBridge) TokenCount() int {
	b.mu.Lock()
	agent := b.agent
	b.mu.Unlock()
	if agent == nil {
		return 0
	}
	return agent.ContextManager().TokenCount()
}

// CurrentSession returns the current session.
func (b *ChatBridge) CurrentSession() *session.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentSes
}

func (b *ChatBridge) PrepareShareBroker(broker *tunnel.Broker, snapshotProvider func() tunnel.BrokerSnapshot) {
	if broker == nil || snapshotProvider == nil {
		return
	}
	b.EnsureSession()
	snapshot := snapshotProvider()
	sessionID := ""
	if current := b.CurrentSession(); current != nil {
		sessionID = current.ID
	}
	replayedCanonical := agentruntime.PublishShareState(broker, sessionID, snapshot, b.CurrentSessionTunnelEvents(), true)
	broker.SetSnapshotProvider(snapshotProvider)
	b.AttachTunnelBroker(broker)
	if !replayedCanonical {
		latest := snapshotProvider()
		if !agentruntime.ShareSnapshotMatches(snapshot, latest) {
			agentruntime.PublishShareState(broker, sessionID, latest, nil, true)
		}
	}
}

// SessionStore returns the session store.
func (b *ChatBridge) SessionStore() session.Store {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessionStore
}

// Resolved returns the resolved endpoint.
func (b *ChatBridge) Resolved() *config.ResolvedEndpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resolved
}

// ResetAgent destroys the current agent, forcing a rebuild on next message.
func (b *ChatBridge) ResetAgent() {
	b.mu.Lock()
	b.agent = nil
	if b.metricCancel != nil {
		b.metricCancel()
	}
	b.metricCollector = nil
	b.metricCancel = nil
	b.mu.Unlock()
}

func (b *ChatBridge) ReasoningEffort() string {
	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()
	if a == nil {
		return ""
	}
	return a.ReasoningEffort()
}

func (b *ChatBridge) CycleReasoningEffort() (string, bool) {
	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()
	if a == nil {
		return "", false
	}
	current := a.ReasoningEffort()
	next := nextReasoningEffort(current)
	if !a.SetReasoningEffort(next) {
		return displayReasoningEffort(current), false
	}
	return displayReasoningEffort(next), true
}

// SwitchModel hot-swaps the model at runtime (mirrors Fyne SwitchModel).
func (b *ChatBridge) SwitchModel(model string) error {
	if model == "" || b.cfg == nil {
		return fmt.Errorf("model is empty or config is nil")
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(b.cfg, b.cfg.Vendor, b.cfg.Endpoint, model)
	if err != nil {
		return fmt.Errorf("activate current selection: %w", err)
	}

	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()

	b.mu.Lock()
	b.resolved = resolved
	b.mu.Unlock()
	agentruntime.ApplyProviderToAgent(a, prov, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(b.cfg, resolved, a, func(resp relaycatalog.ResolveResponse) {
		b.mu.Lock()
		if b.resolved != nil {
			if resp.ContextWindow > 0 {
				b.resolved.ContextWindow = resp.ContextWindow
			}
			if resp.MaxOutputTokens > 0 {
				b.resolved.MaxTokens = resp.MaxOutputTokens
			}
		}
		b.mu.Unlock()
	})
	return nil
}

// OnConfigProviderChanged syncs Wails bridge state after the config tool
// changes vendor/endpoint/model/api_key. Updates b.resolved and b.currentSes
// so the frontend model picker and status bar reflect the new selection.
// Also recreates the provider so the running agent uses the new LLM backend.
func (b *ChatBridge) OnConfigProviderChanged() {
	if b.cfg == nil {
		return
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return
	}
	b.mu.Lock()
	b.resolved = resolved
	if b.currentSes != nil {
		b.currentSes.Vendor = b.cfg.Vendor
		b.currentSes.Endpoint = b.cfg.Endpoint
		b.currentSes.Model = resolved.Model
	}
	b.mu.Unlock()

	// Recreate provider and update agent so it uses the new LLM backend
	resolvedNew, p, err := agentruntime.ResolveCurrentSelection(b.cfg)
	if err != nil {
		return
	}
	b.mu.Lock()
	b.resolved = resolvedNew
	agent := b.agent
	b.mu.Unlock()

	log.Printf("[wails] OnConfigProviderChanged: vendor=%s endpoint=%s model=%s provider=%s agent=%v",
		b.cfg.Vendor, b.cfg.Endpoint, resolvedNew.Model, p.Name(), agent != nil)

	if agent != nil {
		agent.SetProvider(p)
	}

	// Notify Wails frontend to refresh model picker and status bar
	if b.EmitEvent != nil {
		b.EmitEvent("config:updated", nil)
	}
}

// PushErrorToMobile pushes an error message to mobile via tunnel.
func (b *ChatBridge) PushErrorToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushError(msg)
	}
}

// PushSystemMessageToMobile pushes a system message to mobile via tunnel.
func (b *ChatBridge) PushSystemMessageToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushSystemMessage(msg)
	}
}

// PushUserMessageToMobile pushes a user message to mobile via tunnel.
func (b *ChatBridge) PushUserMessageToMobile(msg string) {
	if broker := b.currentTunnelBroker(); broker != nil {
		broker.PushUserMessage(msg)
	}
}

// ResumeSession loads a session and re-initializes the agent for it.
func (b *ChatBridge) ResumeSession(id string) error {
	if err := b.InitAgent(context.Background()); err != nil {
		return err
	}
	if err := b.LoadSession(id); err != nil {
		return err
	}
	return nil
}

// SendContent sends multimodal content to the agent.
func (b *ChatBridge) SendContent(content []provider.ContentBlock) error {
	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		return fmt.Errorf("agent is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.usageTurnIndex++
	b.startTime = time.Now()
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.cancel = nil
		b.mu.Unlock()

		if pending, ok := b.drainPending(); ok {
			if b.OnStreamEvent != nil {
				b.OnStreamEvent("pending_consumed", nil)
			}
			if pending.Hidden {
				_ = b.SendHiddenText(pending.Text)
			} else {
				_ = b.SendMessage(pending.Text)
			}
		}
	}()

	if b.agent == nil {
		if err := b.InitAgent(ctx); err != nil {
			return fmt.Errorf("init agent: %w", err)
		}
	}

	if b.currentSes != nil {
		msg := provider.Message{Role: "user", Content: content}
		b.currentSes.Messages = append(b.currentSes.Messages, msg)
		b.currentSes.UpdatedAt = time.Now()
		if b.sessionStore != nil {
			_ = b.sessionStore.Save(b.currentSes)
		}
	}

	return b.agent.RunStreamWithContent(ctx, content, func(ev provider.StreamEvent) {
		b.emit(ev)
	})
}

// GetAvailableModels returns the list of models available for the current endpoint.
func (b *ChatBridge) GetAvailableModels() []string {
	b.mu.Lock()
	resolved := b.resolved
	cfg := b.cfg
	b.mu.Unlock()

	// Try resolved endpoint first
	if resolved != nil && len(resolved.Models) > 0 {
		return resolved.Models
	}

	// Fallback: look up from config vendors
	if cfg != nil {
		if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
				if len(ep.Models) > 0 {
					return ep.Models
				}
			}
		}
		// Last resort: just current model
		if cfg.Model != "" {
			return []string{cfg.Model}
		}
	}
	return nil
}

// refreshSystemPrompt rebuilds and updates the agent's system prompt.
func (b *ChatBridge) refreshSystemPrompt() {
	var autoMem, projectAutoMem *memory.AutoMemory
	if am := memory.NewAutoMemory(); am != nil {
		autoMem = am
	}
	if pam := memory.NewProjectAutoMemory(b.workingDir); pam != nil {
		projectAutoMem = pam
	}
	startupAssets := agentruntime.LoadInteractiveStartupAssets(b.workingDir, autoMem, projectAutoMem)
	b.mu.Lock()
	mode := b.permissionMode
	b.mu.Unlock()
	newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, mode, autoMem, projectAutoMem, startupAssets.CommandManager)
	b.mu.Lock()
	if b.agent != nil {
		b.agent.UpdateSystemPrompt(newPrompt)
	}
	b.mu.Unlock()
}
