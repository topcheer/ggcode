// Package wailskit provides a public facade for the Wails desktop app.
package wailskit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/cost"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		return "auto (adaptive)"
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
	// pendingSource/pendingExclude carry the ORIGIN source and echo-exclusion
	// adapter PER QUEUED MESSAGE (#461/#475) — restored in order when the
	// queue drains. The old single pendingExclude field mispaired adapters
	// under a multi-message backlog, and the drain path's hardcoded
	// src="mobile" made the exclude value dead code anyway (the consumer
	// requires source=="im").
	pendingSource  []string
	pendingExclude []string

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

	// Idempotency guard for finishRun
	finished bool

	// #1181: watermark of how many of this run's AddedSinceRunStart messages
	// have already been appended to ses.Messages by an earlier
	// persistRunMessages call. A duplicate finisher (interleaved Cancel
	// race) persists only the tail produced after the winner's persist
	// instead of dropping it or duplicating messages.
	persistedRunCount int

	// #1181: serializes persistRunMessages against concurrent finishers.
	persistMu sync.Mutex

	// #1182: tunnel push seam (defaults to TunnelHost.PushStreamEvent). Tests
	// inject a blocking push to prove emit no longer holds b.mu across it.
	tunnelPush func(provider.StreamEvent)

	// IM round accumulator for emitter (mirrors Fyne agentBridge.imRound)
	imRound agentruntime.IMRoundState

	// Unified tunnel event management (from InteractiveRuntimeCore.Tunnel)
	tunnelHost *agentruntime.TunnelHost

	// A2A server for agent-to-agent communication on LAN
	a2aServer        *a2a.Server
	a2aRegistry      *a2a.Registry
	a2aRemoteTool    *a2a.RemoteTool
	a2aRefreshCancel context.CancelFunc
	oauthRenewCancel context.CancelFunc
	lanchatHub       *lanchat.Hub

	// Pending approval/ask_user requests from agent
	interactions *agentruntime.InteractionBroker

	// #4151: local generator for approval/ask_user request IDs when no tunnel
	// broker is connected. Distinct prefix from the broker's "msg-N" IDs so
	// the two namespaces never collide.
	localReqSeq atomic.Uint64

	// Callback for emitting events to frontend.
	OnStreamEvent func(eventType string, data json.RawMessage)

	// Callback fired after current session changes so the host can bind IM/runtime state.
	OnSessionChanged func()

	liveHistory []SessionMessage

	// Session lock for preventing concurrent access to the same session.
	sessionLock      *session.SessionLock
	sessionEphemeral bool // true if this session should be deleted when empty
	// deletedSessions is a tombstone set (#305): session IDs deleted via
	// DeleteSession while a run goroutine may still be draining. Late
	// persists targeting a tombstoned ID are refused instead of
	// O_CREATE-resurrecting the deleted JSONL on disk.
	deletedSessions map[string]struct{}

	// runSes is the session snapshot taken at run start, inside the same
	// b.mu critical section that installs b.cancel (#270). Persist paths
	// (persist handler, checkpoint handler, persistRunMessages) must target
	// this snapshot rather than the write-time b.currentSes — a session
	// switch between run start and a late persist would otherwise append
	// the old run's messages to the new session's JSONL (cross-session
	// pollution via SendHiddenText's LAN channel included).
	runSes *session.Session
	// runGeneration increments on every session switch/clear and run
	// start/end (#489): late events from a superseded run compare their
	// captured generation and self-drop instead of leaking into the new
	// session — persist tails via the persist snapshot below, stream
	// events via the per-callback emitIfCurrent guard (#504).
	runGeneration uint64
	// activeRunGen is the generation of the run that most recently started
	// (#550 E1): finishRun compares it against runGeneration and suppresses
	// a superseded (zombie) run's outward run_done / busy-state emissions.
	activeRunGen uint64
	// persistSession/persistGeneration are the closure-effective persist
	// target captured at run start (#489); see setRunPersistSnapshot.
	persistSession    *session.Session
	persistGeneration uint64
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

		modeStr = "supervised"
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
	if b == nil {
		return []swarm.TeamBoardSnapshot{}
	}

	b.mu.Lock()
	swarms := b.swarmMgr
	b.mu.Unlock()
	if swarms == nil {
		return []swarm.TeamBoardSnapshot{}
	}
	return swarms.ListTeamBoards()
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
	b.mu.Lock()
	b.tunnelHost = th
	b.mu.Unlock()
}

// WorkingDir returns the current workspace directory.
func (b *ChatBridge) WorkingDir() string {
	return b.workingDir
}

// GetTunnelHost returns the tunnel host (for StartShare).
func (b *ChatBridge) GetTunnelHost() *agentruntime.TunnelHost {
	b.mu.Lock()
	th := b.tunnelHost
	b.mu.Unlock()
	return th
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
// If asAgent is true, sends from the agent role instead of human.
func (b *ChatBridge) LanChatSend(content, toNodeID, toRole string, asAgent bool) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	ctx := context.Background()
	if toNodeID == "" {
		if asAgent {
			return b.lanchatHub.SendAsAgent(ctx, "", "", content)
		}
		return b.lanchatHub.SendBroadcast(ctx, content, nil)
	}
	if asAgent {
		return b.lanchatHub.SendAsAgent(ctx, toNodeID, toRole, content)
	}
	return b.lanchatHub.SendDirect(ctx, toNodeID, toRole, content, nil)
}

// LanChatSetNick changes the user's nickname and role.
// The nick string may include a role suffix: "name@role".
func (b *ChatBridge) LanChatSetNick(nick string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	n, r, t := lanchat.ParseNickRoleTeam(nick)
	return b.lanchatHub.SetNickRoleTeam(n, r, t)
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

	if msg != nil {
		agentText := fmt.Sprintf("[LAN Chat from %s]: %s", msg.FromNick, msg.Content)
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"text": agentText, "source": "lanchat"})
			b.OnStreamEvent("user_message", raw)
		}
		return b.SendHiddenText(agentText)
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

// LanChatSetApprovalPolicy sets the approval policy for a peer (by nick).
// policy: "always" (auto-approve), "never" (auto-reject), "" (ask).
func (b *ChatBridge) LanChatSetApprovalPolicy(peerNick string, policy string) error {
	if b.lanchatHub == nil {
		return fmt.Errorf("LAN chat not available")
	}
	b.lanchatHub.SetApprovalPolicy(peerNick, policy)
	return nil
}

// LanChatApprovalPolicies returns all persisted approval policies.
func (b *ChatBridge) LanChatApprovalPolicies() (map[string]string, error) {
	if b.lanchatHub == nil {
		return nil, fmt.Errorf("LAN chat not available")
	}
	return b.lanchatHub.GetApprovalPolicies(), nil
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

	b.mu.Lock()
	if b.cancel != nil {

		meta := &data
		if source == "desktop" {
			meta = nil
		}

		b.pendingMsgs.Enqueue(userMsg, false, meta)
		b.pendingSource = append(b.pendingSource, source)
		b.pendingExclude = append(b.pendingExclude, excludeAdapter)
		b.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.finished = false
	b.persistedRunCount = 0
	b.usageTurnIndex++
	b.startTime = time.Now()

	b.runGeneration++
	b.activeRunGen = b.runGeneration
	runGen := b.runGeneration
	turnID, _ := b.startDesktopTurnLocked()
	b.runSes = b.currentSes
	b.mu.Unlock()

	if b.OnStreamEvent != nil && source != "desktop" {
		raw, _ := json.Marshal(map[string]string{"turn_id": turnID, "message_id": fmt.Sprintf("user-%s", turnID), "text": userMsg, "source": source})
		b.OnStreamEvent("user_message", raw)
	}

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
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushSystemMessage("Processing queued message...")
				}
				data := tunnel.MessageData{Text: pending.Text}
				src := "desktop"
				exclude := ""
				if pending.Meta != nil {
					data = *pending.Meta
					src = "mobile"
				}

				b.mu.Lock()
				if len(b.pendingSource) > 0 {
					src = b.pendingSource[0]
					b.pendingSource = b.pendingSource[1:]
				}
				if len(b.pendingExclude) > 0 {
					exclude = b.pendingExclude[0]
					b.pendingExclude = b.pendingExclude[1:]
				}
				b.mu.Unlock()
				_ = b.sendMessageData(data, src, exclude)
			}
		}
	}()

	if b.agent == nil {

		b.mu.Lock()
		ses := b.currentSes
		mismatch := b.sessionLockMismatchLocked(ses)
		b.mu.Unlock()
		if mismatch {
			err := fmt.Errorf("session %s lock mismatch; refusing agent auto-rebuild", ses.ID)
			b.finishRun(err)
			return err
		}
		if err := b.InitAgent(ctx); err != nil {

			b.finishRun(err)
			return fmt.Errorf("init agent: %w", err)
		}
	}

	if err := b.ensureSession(); err != nil {

		b.finishRun(err)
		return fmt.Errorf("ensure session: %w", err)
	}

	b.mu.Lock()
	b.runSes = b.currentSes
	b.mu.Unlock()

	b.refreshPersistTargetIfOwner(runGen)

	b.bindTunnelProjectionSession()
	b.appendLiveUserMessage(userMsg)

	if broker := b.currentTunnelBroker(); broker != nil && source == "desktop" {
		if strings.TrimSpace(data.MessageID) == "" {
			data.MessageID = broker.NextMessageID()
		}
		broker.PushUserMessageData(data)
		broker.PushStatus(tunnel.StatusBusy, "")
		b.resetTunnelRoundState()
	}

	if b.Emitter != nil {
		if source == "im" && excludeAdapter != "" {
			b.Emitter.EmitUserTextExcept(userMsg, excludeAdapter)
		} else if source != "im" {
			b.Emitter.EmitUserText(userMsg)
		}
	}

	if b.lanchatHub != nil {
		b.lanchatHub.SetAgentBusy(true)
	}

	err := b.agent.RunStream(ctx, userMsg, func(ev provider.StreamEvent) {

		b.emitIfCurrent(runGen, ev)
	})
	if err != nil && !errors.Is(err, context.Canceled) {

		b.appendLiveErrorIfCurrent(runGen, err.Error())
	}
	b.finishRun(err)

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

	b.mu.Lock()
	subAgents := b.subAgentMgr
	swarms := b.swarmMgr
	b.mu.Unlock()
	if subAgents != nil {
		subAgents.CancelAll()
	}
	if swarms != nil {
		swarms.CancelAll()
	}

	if b.OnStreamEvent != nil {
		b.OnStreamEvent("approval:cancel", json.RawMessage(`{}`))
		b.OnStreamEvent("ask_user:cancel", json.RawMessage(`{}`))
	}

	b.finishRun(context.Canceled)
}

// finishRun performs all post-run cleanup: flush tunnel text, push idle
// status to mobile, emit run_done to frontend, and notify LAN Chat peers.
// err is the agent run result (may be nil for success or context.Canceled).
func (b *ChatBridge) finishRun(err error) {

	b.mu.Lock()
	if b.finished {
		b.mu.Unlock()

		b.persistRunMessages()
		return
	}
	b.finished = true

	b.finalizeStreamingToolsLocked()

	superseded := b.activeRunGen != 0 && b.activeRunGen != b.runGeneration
	b.mu.Unlock()

	b.persistRunMessages()

	if superseded {
		debug.Log("wailskit", "suppressed superseded run's run_done/busy emissions (#550 E1)")
		if b.metricCollector != nil {
			b.metricCollector.Flush()
		}
		return
	}

	if broker := b.currentTunnelBroker(); broker != nil {
		b.flushTunnelTextStream(broker, false)
		broker.PushStatus(tunnel.StatusIdle, "")
		broker.PushActivity("")
	}

	if b.lanchatHub != nil {
		b.lanchatHub.SetAgentBusy(false)

		if err == nil {
			b.lanchatHub.ReportAgentSuccess()
		} else if !errors.Is(err, context.Canceled) {
			b.lanchatHub.ReportAgentFailure(provider.ClassifyLLMError(err))
		}
	}

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
		if err != nil && !errors.Is(err, context.Canceled) {
			raw, _ = json.Marshal(map[string]interface{}{"turn_id": turnID, "message_id": assistantID, "error": err.Error()})
		}
		b.OnStreamEvent("run_done", raw)
	}
}

// ClearCurrentSession resets the current session so next chat creates a fresh one.
// refreshPersistTargetIfOwner re-binds the per-run persist target while this
// run is still the generation owner (#4277). It never bumps runGeneration:
// the entry bump claimed ownership, and a superseded (Cancel-blind) run
// re-bumping here would push the generation past a newer run's captured
// runGen, silencing all of its stream events via the emitIfCurrent stale
// drop. Mirrors the #1181 owner guard in SendContent.
func (b *ChatBridge) refreshPersistTargetIfOwner(runGen uint64) {
	b.mu.Lock()
	if b.activeRunGen == runGen {
		b.persistSession = b.runSes
	}
	b.mu.Unlock()
}

// setRunPersistSnapshot installs the per-run persist target for THIS run
// only: a captured session + generation (#489). Replaces read-at-trigger
// semantics for run-scoped persists; called after runSes is established.
func (b *ChatBridge) setRunPersistSnapshot() {
	b.mu.Lock()
	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.persistSession = b.runSes
	b.mu.Unlock()
}

// currentRunGeneration returns the run generation under the lock.
func (b *ChatBridge) currentRunGeneration() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runGeneration
}

// emitIfCurrent forwards ev to emit() only when the calling run's captured
// generation is still current (#504): a superseded run (session cleared via
// ClearCurrentSession, or a newer run started) drops its late stream events
// instead of leaking them into the new session's live history and frontend
// stream. This is the wired form of the guard #489's message claimed —
// emitGeneration was declared but never compared, leaving emit() unguarded.
func (b *ChatBridge) emitIfCurrent(gen uint64, ev provider.StreamEvent) {
	b.mu.Lock()
	stale := gen != b.runGeneration
	b.mu.Unlock()
	if stale {
		debug.Log("wailskit", "drop stale-run stream event (type=%d)", ev.Type)
		return
	}
	b.emit(ev)
}

func (b *ChatBridge) ClearCurrentSession() error {

	b.mu.Lock()
	busy := b.cancel != nil
	b.mu.Unlock()
	if busy {
		debug.Log("wailskit", "ClearCurrentSession refused: agent run in progress (#550 E1)")
		return fmt.Errorf("agent run in progress; cancel the run before clearing the session")
	}

	b.cleanupEphemeralSession()

	b.mu.Lock()
	b.runGeneration++
	b.persistSession = nil
	b.mu.Unlock()

	state := agentruntime.ClearSession()
	b.ResetAgent()
	b.mu.Lock()

	b.runSes = nil
	b.currentSes = state.Session
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	if b.currentSes != nil {

		b.liveHistory = mergeTunnelUserMessages(
			buildSessionHistoryFromMessages(b.currentSes.Messages),
			b.currentSes.SnapshotTunnelEvents(),
		)
	} else {
		b.liveHistory = nil
	}
	b.metricEvents = nil
	if b.tunnelHost != nil {
		b.tunnelHost.ResetStreamState()
	}
	b.mu.Unlock()
	b.bindSessionIntegrations(nil)
	return nil
}

// cleanupEphemeralSession deletes the current session if it was marked
// ephemeral (auto-created because the latest session was locked) and
// has no user messages. Also releases the session lock.
func (b *ChatBridge) cleanupEphemeralSession() {
	b.mu.Lock()
	ses := b.currentSes
	ephemeral := b.sessionEphemeral
	store := b.sessionStore
	lock := b.sessionLock
	b.mu.Unlock()

	if ephemeral && ses != nil && store != nil {
		if err := agentruntime.DeleteSessionIfEmpty(store, ses); err != nil {
			log.Printf("[chat] cleanupEphemeralSession: delete failed: %v", err)
		} else {

			im.ClearSessionBindingsGlobal(ses.ID)
		}
	}

	if lock != nil {
		lock.Release()
	}
	b.cleanupEphemeralFinalize(ses, lock)
}

// cleanupEphemeralFinalize is the post-release critical section of
// cleanupEphemeralSession, split out so its ownership re-checks are directly
// testable. Both re-checks are required: while lock.Release() was blocked
// outside b.mu, a concurrent EnsureSession may have installed a new session
// and/or lock — clearing unconditionally would clobber that new state (#279).
func (b *ChatBridge) cleanupEphemeralFinalize(ses *session.Session, lock *session.SessionLock) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sessionLock == lock {
		b.sessionLock = nil

		if b.currentSes == ses {
			b.sessionEphemeral = false
		}
	}
}

// prevSessionState is a snapshot of the bridge's session fields taken before
// a LoadSession switch attempt, used to roll back if the switch fails (#269).
type prevSessionState struct {
	ses              *session.Session
	ephemeral        bool
	usageTurnIndex   int
	lastMetricDigest int
	// deletedByCleanup is true when cleanupEphemeralSession removed ses from
	// the store (ephemeral + no messages) before the switch attempt — such a
	// session cannot be rolled back to.
	deletedByCleanup bool
}

// snapshotSessionState captures the current session state for a possible
// rollback in LoadSession (#269). Must be called before cleanupEphemeralSession.
func (b *ChatBridge) snapshotSessionState() prevSessionState {
	b.mu.Lock()
	defer b.mu.Unlock()
	ses := b.currentSes
	eph := b.sessionEphemeral

	deleted := eph && ses != nil && len(ses.Messages) == 0
	return prevSessionState{
		ses:              ses,
		ephemeral:        eph,
		usageTurnIndex:   b.usageTurnIndex,
		lastMetricDigest: b.lastMetricDigestTurn,
		deletedByCleanup: deleted,
	}
}

// rollbackSessionLoad restores the bridge to the session state captured by
// snapshotSessionState after a failed LoadSession switch (#269). Without the
// rollback, b.currentSes keeps pointing at the failed session whose lock was
// just released, so CurrentSessionID() lies and the SendContent auto-rebuild
// path would cross-append to a JSONL another instance may now own.
//
// If the previous session no longer exists on disk (ephemeral cleanup) or its
// lock cannot be re-acquired, the current session is cleared instead — b.currentSes
// must never point at a session whose lock we do not hold.
func (b *ChatBridge) rollbackSessionLoad(from *session.Session, prev prevSessionState) {

	b.mu.Lock()
	cur := b.currentSes
	b.mu.Unlock()
	if cur != from {
		return
	}
	if prev.ses != nil && !prev.deletedByCleanup {

		storeDir, _ := session.DefaultDir()
		lock, lockErr := session.TryAcquireSessionLock(storeDir, prev.ses.ID)
		if lockErr == nil && lock.Acquired() {
			b.mu.Lock()
			if b.sessionLock == nil {
				b.sessionLock = lock
				b.sessionEphemeral = prev.ephemeral
				b.mu.Unlock()
			} else {
				b.mu.Unlock()
				lock.Release()
				b.setSessionState(agentruntime.ClearSession())
				log.Printf("[chat] LoadSession: rollback skipped, new lock installed; current session cleared")
				return
			}
			b.setSessionState(agentruntime.SessionState{
				Session:              prev.ses,
				UsageTurnIndex:       prev.usageTurnIndex,
				LastMetricDigestTurn: prev.lastMetricDigest,
			})
			log.Printf("[chat] LoadSession: rolled back to previous session %s after init failure", prev.ses.ID)
			return
		}
		log.Printf("[chat] LoadSession: previous session %s is locked elsewhere; clearing current session", prev.ses.ID)
	}

	b.mu.Lock()
	b.sessionEphemeral = false
	b.mu.Unlock()
	b.setSessionState(agentruntime.ClearSession())
}

// sessionLockMismatchLocked reports whether the held session lock (if any)
// belongs to a session other than ses — positive evidence that ses is no
// longer ours to write (#269). Callers must hold b.mu. A nil lock is NOT a
// mismatch: lock acquisition is best-effort on some session creation paths
// (lowercase ensureSession), and absence of a lock is not evidence of one.
func (b *ChatBridge) sessionLockMismatchLocked(ses *session.Session) bool {
	if ses == nil || b.sessionLock == nil {
		return false
	}
	return b.sessionLock.SessionID() != ses.ID
}

// MarkSessionDeleted records id in the bridge's tombstone set so that late
// persist attempts from a draining run goroutine are refused instead of
// O_CREATE-resurrecting the just-deleted session on disk (#305).
func (b *ChatBridge) MarkSessionDeleted(id string) {
	if id == "" {
		return
	}
	b.mu.Lock()
	if b.deletedSessions == nil {
		b.deletedSessions = make(map[string]struct{})
	}
	b.deletedSessions[id] = struct{}{}
	b.mu.Unlock()
}

// appendPersistMessage appends msg to ses's JSONL under the session-lock
// guard (#269): when the bridge holds a lock for a different session, ses is
// no longer ours to write (e.g. its lock was released after a failed load) —
// refuse the append rather than risk cross-process double-writes to the
// same JSONL. Deleted sessions (tombstoned via MarkSessionDeleted) are also
// refused so a draining run goroutine cannot resurrect them (#305).
func (b *ChatBridge) appendPersistMessage(store *session.JSONLStore, ses *session.Session, msg provider.Message) {
	if ses == nil {
		return
	}
	b.mu.Lock()
	mismatch := b.sessionLockMismatchLocked(ses)
	_, tombstoned := b.deletedSessions[ses.ID]
	lockID := ""
	if b.sessionLock != nil {
		lockID = b.sessionLock.SessionID()
	}
	b.mu.Unlock()
	if tombstoned {
		log.Printf("[chat] persist: refusing write to deleted session %s (#305)", ses.ID)
		return
	}
	if mismatch {
		log.Printf("[chat] persist: refusing write to session %s while lock held for %s (#269)", ses.ID, lockID)
		return
	}
	if err := store.AppendMessageToDisk(ses, msg); err != nil {
		log.Printf("[chat] persist handler: AppendMessageToDisk failed: %v", err)
	}
}

// persistUserMessageRemoval appends a message_remove tombstone to the run's
// JSONL (#4200). It is the journal-side compensation for
// Manager.RemoveLastUserMessage in the vision-fallback retry path: the first
// RunStreamWithContent persisted the ORIGINAL image content via the persist
// handler, so dropping it from live context alone left the journal replaying
// both the original and the text-only projection on resume. Guard discipline
// mirrors appendPersistMessage (run-scoped persistSession snapshot, #269 lock
// mismatch, #305 deleted-session tombstone).
func (b *ChatBridge) persistUserMessageRemoval() {
	b.mu.Lock()
	ses := b.persistSession
	mismatch := b.sessionLockMismatchLocked(ses)
	// #4251: nil check BEFORE the deletedSessions lookup - ses.ID dereferences
	// the snapshot, and ClearCurrentSession can leave persistSession nil while
	// a draining vision-fallback run still reaches this point (stop + new
	// session sequence). Mirrors the persist handler's ordering.
	var deleted bool
	if ses != nil {
		_, deleted = b.deletedSessions[ses.ID]
	}
	store := b.sessionStore
	b.mu.Unlock()
	if ses == nil || mismatch || deleted {
		return
	}
	jsonlStore, ok := store.(*session.JSONLStore)
	if !ok {
		return
	}
	if err := jsonlStore.AppendUserMessageRemovalToDisk(ses); err != nil {
		log.Printf("[chat] persist: AppendUserMessageRemovalToDisk failed: %v", err)
	}
}

func (b *ChatBridge) setSessionState(state agentruntime.SessionState) {
	b.mu.Lock()

	if state.Session != nil {
		delete(b.deletedSessions, state.Session.ID)
	}
	b.currentSes = state.Session
	ag := b.agent
	sid := ""
	if b.currentSes != nil {
		sid = b.currentSes.ID
	}
	if ag != nil && sid != "" {

		defer func() { ag.SetSessionID(sid) }()
	}
	b.usageTurnIndex = state.UsageTurnIndex
	b.lastMetricDigestTurn = state.LastMetricDigestTurn
	b.liveHistory = nil
	b.metricEvents = nil
	if b.currentSes != nil {

		b.liveHistory = mergeTunnelUserMessages(
			buildSessionHistoryFromMessages(b.currentSes.Messages),
			b.currentSes.SnapshotTunnelEvents(),
		)
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
	lanChatHub := b.lanchatHub
	b.mu.Unlock()

	if tunnelHost != nil && ses != nil && store != nil {
		tunnelHost.BindSession(ses, store)
	}
	if lanChatHub != nil && ses != nil {
		lanChatHub.SetSessionID(
			filepath.Join(config.ConfigDir(), "lanchat"),
			ses.ID,
		)

		if b.EmitEvent != nil {
			b.EmitEvent("lanchat:identity_updated", map[string]string{
				"session_id": ses.ID,
			})
		}
	}
	if b.cronScheduler != nil && ses != nil {
		sessionPath, legacyPath := agentruntime.CronStorePaths(ses.ID)
		b.cronScheduler.SwitchSession(sessionPath, legacyPath, b.workingDir)
	}
	if onSessionChanged != nil {
		onSessionChanged()
	}
}

func (b *ChatBridge) LoadSession(id string) error {
	if b.sessionStore == nil {
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("init session store: %w", err)
		}
		b.sessionStore = store
	}

	b.mu.Lock()
	busy := b.cancel != nil
	b.mu.Unlock()
	if busy {
		return fmt.Errorf("session switch while agent is running")
	}

	prev := b.snapshotSessionState()

	b.cleanupEphemeralSession()

	storeDir, _ := session.DefaultDir()
	lock, lockErr := session.TryAcquireSessionLock(storeDir, id)
	if lockErr != nil || lock == nil || !lock.Acquired() {

		b.rollbackSessionLoad(prev.ses, prev)
		if cb := b.OnSessionChanged; cb != nil {
			cb()
		}
		return fmt.Errorf("session is locked by another instance: %s", id)
	}
	b.mu.Lock()
	b.sessionLock = lock
	store := b.sessionStore
	b.mu.Unlock()

	state, err := agentruntime.LoadSession(store, id)
	if err != nil {

		b.mu.Lock()
		ours := b.sessionLock == lock
		if ours {
			b.sessionLock = nil
		}
		b.mu.Unlock()
		if ours {
			lock.Release()
		}

		b.rollbackSessionLoad(prev.ses, prev)
		if cb := b.OnSessionChanged; cb != nil {
			cb()
		}
		return fmt.Errorf("load session: %w", err)
	}

	b.mu.Lock()
	busy = b.cancel != nil
	b.mu.Unlock()
	if busy {
		b.mu.Lock()
		ours := b.sessionLock == lock
		if ours {
			b.sessionLock = nil
		}
		b.mu.Unlock()
		if ours {
			lock.Release()
		}

		b.rollbackSessionLoad(prev.ses, prev)
		if cb := b.OnSessionChanged; cb != nil {
			cb()
		}
		return fmt.Errorf("session switch while agent is running")
	}

	b.mu.Lock()
	b.runGeneration++
	b.mu.Unlock()
	b.ResetAgent()
	b.setSessionState(state)
	if err := b.InitAgent(context.Background()); err != nil {

		b.mu.Lock()
		ours := b.sessionLock == lock
		if ours {
			b.sessionLock = nil
		}
		onSessionChanged := b.OnSessionChanged
		b.mu.Unlock()
		if ours {
			lock.Release()
		}

		b.ResetAgent()
		b.rollbackSessionLoad(state.Session, prev)
		if onSessionChanged != nil {
			onSessionChanged()
		}
		return fmt.Errorf("init agent for session load: %w", err)
	}
	_, _, _ = agentruntime.RestoreSessionIntoAgent(b.agent, state.Session)

	if state.Session.PermissionMode != "" {
		sessionMode := permission.ParsePermissionMode(state.Session.PermissionMode)
		b.mu.Lock()
		b.permissionMode = sessionMode
		agent := b.agent
		b.mu.Unlock()
		if agent != nil {
			policy := permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, sessionMode)
			agent.SetPermissionPolicy(policy)
		}
		b.refreshSystemPrompt()
	}

	if state.Session.Model != "" {
		sesModel := state.Session.Model
		sesVendor := state.Session.Vendor
		sesEndpoint := state.Session.Endpoint

		if !sessionSelectionMatches(b.resolved, sesVendor, sesEndpoint, sesModel) {
			resolved, prov, err := agentruntime.ActivateCurrentSelection(b.cfg, sesVendor, sesEndpoint, sesModel)
			if err == nil {
				b.mu.Lock()
				b.resolved = resolved
				agent := b.agent
				b.mu.Unlock()
				agentruntime.ApplyProviderToAgent(agent, prov, resolved)
			} else {

				debug.Log("chat", "LoadSession: restoring session model %q (vendor=%q endpoint=%q) failed: %v - keeping InitAgent's current model", sesModel, sesVendor, sesEndpoint, err)
			}
		}
	}

	if b.agent != nil && b.agent.ContextManager() != nil {
		if state.Session.ContextWindow > 0 {
			b.agent.ContextManager().SetContextWindow(state.Session.ContextWindow)
			debug.Log("chat", "LoadSession: restored context_window=%d from session", state.Session.ContextWindow)
		}
		if state.Session.MaxTokens > 0 {
			b.agent.ContextManager().SetOutputReserve(state.Session.MaxTokens)
			debug.Log("chat", "LoadSession: restored max_tokens=%d from session", state.Session.MaxTokens)
		}
	}

	return nil
}

// ensureSession creates a new session if none exists (mirrors Fyne bridge).
// EnsureSession creates a new session if one doesn't already exist.
// Called on startup and before sending messages.
func (b *ChatBridge) ensureSession() error {

	b.mu.Lock()
	if b.sessionStore == nil {
		b.mu.Unlock()
		store, err := session.NewDefaultStore()
		if err != nil {
			return fmt.Errorf("create session store: %w", err)
		}
		b.mu.Lock()
		b.sessionStore = store
	}
	store := b.sessionStore
	current := b.currentSes
	cfg := b.cfg
	workingDir := b.workingDir
	b.mu.Unlock()
	vendor, endpoint, model := "", "", ""
	if cfg != nil {
		vendor = cfg.Vendor
		endpoint = cfg.Endpoint
		model = cfg.Model
	}
	state, created, err := agentruntime.EnsureSession(store, current, vendor, endpoint, model, workingDir)
	if err != nil {
		return fmt.Errorf("save new session: %w", err)
	}
	if created {
		b.setSessionState(state)
	}
	return nil
}

// persistRunMessages updates in-memory session state after an agent run.
// With per-message persistence (SetPersistHandler), each message is already
// written to JSONL at Add() time. This only updates ses.Messages for rendering.
func (b *ChatBridge) persistRunMessages() {

	b.persistMu.Lock()
	defer b.persistMu.Unlock()

	b.mu.Lock()

	ses := b.runSes
	if ses == nil {
		ses = b.currentSes
	}
	cur := b.currentSes
	ag := b.agent
	skip := b.persistedRunCount
	b.mu.Unlock()

	if ses == nil || ag == nil {
		return
	}
	if cur != nil && cur != ses {
		log.Printf("[chat] persistRunMessages: session switched mid-run (run session=%s, current=%s); run messages kept in the run's session only (#270)", ses.ID, cur.ID)
	}

	runAdded := ag.AddedSinceRunStart()
	newMsgs := runMessagesToPersist(runAdded, skip)

	b.mu.Lock()
	ses.Messages = append(ses.Messages, newMsgs...)
	ses.UpdatedAt = time.Now()
	b.persistedRunCount = len(runAdded)
	b.mu.Unlock()
}

// runMessagesToPersist returns the messages still to be appended for this
// run. First persist (skip == 0) drops the seed user message (written at
// run start); subsequent persists (#1181 watermark) return only the tail
// after what an earlier finisher already appended.
func runMessagesToPersist(runAdded []provider.Message, skip int) []provider.Message {
	if skip > 0 {
		if skip > len(runAdded) {
			skip = len(runAdded)
		}
		return runAdded[skip:]
	}
	if len(runAdded) > 0 && runAdded[0].Role == "user" {
		return runAdded[1:]
	}
	return runAdded
}

func (b *ChatBridge) StartNewSession() (string, error) {

	b.mu.Lock()
	busy := b.cancel != nil
	b.mu.Unlock()
	if busy {
		b.Cancel()
	}
	if err := b.ClearCurrentSession(); err != nil {
		return "", err
	}
	if err := b.ensureSession(); err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes == nil {
		return "", fmt.Errorf("new session was not initialized")
	}

	storeDir, _ := session.DefaultDir()
	lock, _ := session.TryAcquireSessionLock(storeDir, b.currentSes.ID)
	if lock != nil && lock.Acquired() {
		b.sessionLock = lock
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

// syncRenamedTitle mirrors an on-disk rename (RenameSession) into the
// in-memory current session (#628). Meta write-backs such as usage persists,
// SetSessionLimits, and SetPermissionMode serialize b.currentSes via
// AppendMetaToDisk; without this sync the stale pre-rename title gets written
// back and silently rolls back the rename on disk.
func (b *ChatBridge) syncRenamedTitle(id, title string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.currentSes != nil && b.currentSes.ID == id {
		b.currentSes.Title = title
	}
}

// EnsureSession creates a default session if none exists (mirrors Fyne's ensureSession).
// On first call with no current session, tries to auto-load the most recent
// workspace session. If that session is locked by another instance, creates
// a new ephemeral session (auto-deleted if empty on close/switch).
func (b *ChatBridge) EnsureSession() {

	b.mu.Lock()
	if b.sessionStore == nil {
		b.mu.Unlock()
		store, err := session.NewDefaultStore()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.sessionStore = store
	}
	if b.currentSes != nil {
		b.mu.Unlock()
		return
	}
	store := b.sessionStore
	workingDir := b.workingDir
	cfg := b.cfg
	b.mu.Unlock()

	if sessions, err := store.ListForWorkspace(workingDir); err == nil && len(sessions) > 0 {
		storeDir, _ := session.DefaultDir()
		for _, s := range sessions {

			full, loadErr := store.Load(s.ID)
			if loadErr != nil || full == nil {
				continue
			}
			if len(full.Messages) == 0 {
				continue
			}
			lock, lockErr := session.TryAcquireSessionLock(storeDir, s.ID)
			if lockErr != nil || lock == nil || !lock.Acquired() {
				continue
			}

			b.mu.Lock()
			if b.currentSes != nil {
				b.mu.Unlock()
				lock.Release()
				return
			}
			b.sessionLock = lock
			b.sessionEphemeral = false
			b.mu.Unlock()
			b.setSessionState(agentruntime.AdoptSession(full))
			return
		}
	}

	vendor, endpoint, model := "", "", ""
	if cfg != nil {
		vendor = cfg.Vendor
		endpoint = cfg.Endpoint
		model = cfg.Model
	}
	b.mu.Lock()
	current := b.currentSes
	b.mu.Unlock()
	state, created, err := agentruntime.EnsureSession(store, current, vendor, endpoint, model, workingDir)
	if err != nil || !created {
		log.Printf("[chat] EnsureSession: FAILED to create new session: err=%v created=%v", err, created)
		return
	}

	b.mu.Lock()
	if b.currentSes != nil && b.currentSes != state.Session {
		b.mu.Unlock()

		_ = agentruntime.DeleteSessionIfEmpty(store, state.Session)
		return
	}
	b.sessionEphemeral = true
	ag := b.agent
	b.mu.Unlock()
	if ag != nil {

		ag.SetSessionID(state.Session.ID)
	}
	log.Printf("[chat] EnsureSession: created new session %s", state.Session.ID)
	b.setSessionState(state)

	storeDir, _ := session.DefaultDir()
	b.mu.Lock()
	ses := b.currentSes
	b.mu.Unlock()
	if ses != nil {
		lock, _ := session.TryAcquireSessionLock(storeDir, ses.ID)
		if lock != nil && lock.Acquired() {
			b.mu.Lock()
			if b.sessionLock == nil {
				b.sessionLock = lock
				b.mu.Unlock()
			} else {
				b.mu.Unlock()
				lock.Release()
			}
		}
	}
}

// InitAgent sets up provider, tools, and agent — full parity with Fyne bridge.
// Called on startup or before the first message if not yet initialized.
func (b *ChatBridge) InitAgent(_ ...context.Context) error {

	agentruntime.InitGlobalSectionCollector(b.workingDir)

	mode := agentruntime.InteractivePermissionModeWithDefault(b.cfg, false, "supervised")
	b.permissionMode = mode
	policy := agentruntime.BuildInteractivePermissionPolicy(b.cfg, b.workingDir, false)

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

	if sm, ok := core.Registry.Get("switch_mode"); ok {
		if smt, ok := sm.(*tool.SwitchModeTool); ok {
			smt.SetSwitcher(&desktopModeSwitcher{bridge: b})
		}
	}

	b.cronScheduler = agentruntime.NewSessionCronScheduler("", b.workingDir, func(prompt string, queueIfBusy bool) {
		if !queueIfBusy && b.IsWorking() {
			log.Printf("[cron] skipping prompt (agent busy, queue_if_busy=false): %s", prompt)
			return
		}
		log.Printf("[cron] firing prompt (queue_if_busy=%v): %s", queueIfBusy, prompt)
		b.sendMessageData(tunnel.MessageData{Text: prompt}, "cron", "")
	})
	agentruntime.RegisterCronTools(b.registry, b.cronScheduler)
	mcpMgr := core.MCPManager
	b.mcpManager = mcpMgr

	if mcpMgr != nil {
		mcpMgr.SetOnUpdate(func(servers []plugin.MCPServerInfo) {
			raw, _ := json.Marshal(servers)
			if b.OnStreamEvent != nil {
				b.OnStreamEvent("mcp:status", raw)
			}
		})
	}

	core.StartBackgroundServices()

	b.mu.Lock()
	oldHost := b.tunnelHost

	b.tunnelHost = core.Tunnel
	currentSes := b.currentSes
	b.mu.Unlock()

	if oldHost != nil {
		oldHost.Close()
	}
	if currentSes != nil {
		b.bindSessionIntegrations(currentSes)
	}
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	commandMgr := core.CommandManager
	saveMemoryTool := core.SaveMemoryTool

	saveMemoryTool.SetAfterSave(func() {
		newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
		b.mu.Lock()
		if b.agent != nil {
			b.agent.UpdateSystemPrompt(newPrompt)
		}
		b.mu.Unlock()
	})

	if core.DeleteMemoryTool != nil {
		core.DeleteMemoryTool.SetAfterSave(func() {
			newPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
			b.mu.Lock()
			if b.agent != nil {
				b.agent.UpdateSystemPrompt(newPrompt)
			}
			b.mu.Unlock()
		})
	}

	if b.acpClientMgr != nil {
		b.acpClientMgr.CloseAll()
	}
	b.acpClientMgr = acpclient.NewClientManager(b.workingDir, policy)
	b.acpClientMgr.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {

		return b.RequestApproval(ctx, b.nextInteractionRequestID(), toolName, input)
	})

	agentFactory := func(prov provider.Provider, t interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, t.(*tool.Registry), systemPrompt, maxTurns)
	}

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

	subAgents := agentruntime.NewSubAgentManager(b.cfg.SubAgents, b.registry, p, func() provider.Provider {
		if b.agent != nil {
			return b.agent.Provider()
		}
		return p
	}, func() []string {
		if b.cfg != nil {
			if vc, ok := b.cfg.Vendors[b.cfg.Vendor]; ok {
				if ep, ok := vc.Endpoints[b.cfg.Endpoint]; ok {
					return ep.Models
				}
			}
		}
		return nil
	}, b.workingDir, func(usage provider.TokenUsage) { b.recordSessionUsage(usage, "subagent") }, b.metricCollector.Emit, agentFactory, subAgentPromptBuilder)

	b.mu.Lock()
	b.subAgentMgr = subAgents
	b.mu.Unlock()
	_ = b.registry.Register(agentruntime.NewSkillTool(commandMgr, mcpMgr, p, b.registry, agentFactory, b.workingDir, func(usage provider.TokenUsage) { b.recordSessionUsage(usage, "subagent") }, b.skillOnMetric, subAgentPromptBuilder))
	_ = b.registry.Register(tool.CreateSkillTool{CommandMgr: commandMgr, WorkingDir: b.workingDir})
	agentruntime.RegisterDelegateTool(b.registry, b.acpClientMgr, func() *subagent.Manager {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.subAgentMgr
	}, b.workingDir, func() string {
		if b.agent != nil {
			return b.agent.WorkingDir()
		}
		return b.workingDir
	})

	subAgents.SetOnStreamText(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_text", raw)
		agentruntime.PushTunnelSubagentText(b.currentTunnelBroker, agentID, text)
	})
	subAgents.SetOnReasoning(func(agentID, text string) {
		if b.OnStreamEvent == nil {
			agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
			return
		}
		raw, _ := json.Marshal(map[string]string{"agentID": agentID, "title": b.subagentPanelTitle(agentID), "content": text})
		b.OnStreamEvent("subagent_reasoning", raw)
		agentruntime.PushTunnelSubagentReasoning(b.currentTunnelBroker, agentID, text)
	})
	subAgents.SetOnToolCall(func(agentID, toolID, toolName, displayName, args, detail string) {
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
	subAgents.SetOnToolResult(func(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
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

	subAgents.SetOnComplete(func(sa *subagent.SubAgent) {
		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]interface{}{
				"agentID": sa.ID,
				"title":   b.subagentPanelTitle(sa.ID),
				"isError": sa.Status == subagent.StatusFailed,
			})
			b.OnStreamEvent("subagent_done", raw)
		}
	})

	swarmFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	toolBuilder := func(allowedTools []string) interface{} {
		cloned := b.registry.Clone()

		for _, name := range []string{
			"ask_user", "spawn_agent", "wait_agent", "list_agents",
			"teammate_spawn", "teammate_shutdown", "team_create", "team_delete",
		} {
			cloned.Unregister(name)
		}
		return cloned
	}
	swarms := agentruntime.NewSwarmManager(b.cfg.Swarm, p, b.registry, nil, swarmFactory, toolBuilder)
	b.mu.Lock()
	b.swarmMgr = swarms
	b.mu.Unlock()
	swarms.SetSystemPromptBuilder(func(name, teamName, wd string) string {
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

	b.registry.Register(tool.SendMessageTool{Manager: subAgents, SwarmMgr: swarms})

	swarms.SetOnUpdate(func(ev swarm.Event) {

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

		if broker := b.currentTunnelBroker(); broker != nil {
			_ = broker
			agentruntime.PushTunnelSwarmEvent(
				b.currentTunnelBroker,
				swarms,
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

	systemPrompt := buildWailsSystemPrompt(b.cfg, b.workingDir, b.permissionMode, autoMem, projectAutoMem, commandMgr)
	maxIter := b.cfg.MaxIterations
	a := agent.NewAgent(p, b.registry, systemPrompt, maxIter)

	if sid := b.CurrentSessionID(); sid != "" {
		a.SetSessionID(sid)
	}
	core.SetConfigAgent(a)
	core.SetConfigUINotify(func() {
		b.OnConfigProviderChanged()
	})
	a.SetPermissionPolicy(policy)
	a.SetHookConfig(b.cfg.Hooks)

	if b.workingDir != "" {
		a.SetReflectionFunc(buildReflectionFunc(b.workingDir))
	}

	a.SetUsageHandler(func(usage provider.TokenUsage) {
		b.recordSessionUsage(usage, a.UsageSource())
	})

	collectorCtx, collectorCancel := context.WithCancel(context.Background())
	b.metricCancel = collectorCancel
	b.metricCollector = metrics.NewCollector(collectorCtx, 256, func(ev metrics.MetricEvent) {
		b.recordMetric(ev)
	})
	a.SetMetricHandler(b.metricCollector.Emit)

	agentruntime.ApplyResolvedLimitsToAgent(a, resolved)
	sessionCW, sessionMT := 0, 0
	if b.currentSes != nil {
		sessionCW = b.currentSes.ContextWindow
		sessionMT = b.currentSes.MaxTokens
	}
	agentruntime.StartAsyncRelayModelLimitRefreshWithSession(b.cfg, resolved, a, sessionCW, sessionMT, func(resp relaycatalog.ResolveResponse) {
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

	a.SetApprovalHandler(func(ctx context.Context, toolName string, input string) permission.Decision {

		return b.RequestApproval(ctx, b.nextInteractionRequestID(), toolName, input)
	})

	if askTool, ok := b.registry.Get("ask_user"); ok {
		if aut, ok := askTool.(*tool.AskUserTool); ok {
			aut.SetHandler(func(ctx context.Context, req tool.AskUserRequest) (tool.AskUserResponse, error) {

				return b.RequestAskUser(ctx, b.nextInteractionRequestID(), req)
			})
		}
	}

	_, _ = agentruntime.ApplyProjectMemoryToAgent(a, b.workingDir)

	b.agent = a

	b.startA2A(b.cfg, a, b.registry)
	b.startOAuthRenewalWatcher()

	a.SetInterruptionHandler(func() []provider.ContentBlock {
		return b.drainPendingInterrupt()
	})
	b.EnsureSession()

	b.mu.Lock()
	ses := b.currentSes
	ag := b.agent
	b.mu.Unlock()
	if ses != nil && ag != nil && len(ses.Messages) > 0 {
		_, _, _ = agentruntime.RestoreSessionIntoAgent(ag, ses)
	}

	b.mu.Lock()
	store := b.sessionStore
	b.mu.Unlock()
	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		ag.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
			b.mu.Lock()

			ses := b.runSes
			if ses == nil {
				ses = b.currentSes
			}
			mismatch := b.sessionLockMismatchLocked(ses)
			b.mu.Unlock()
			if ses == nil || mismatch {
				return
			}
			if err := jsonlStore.AppendCheckpointToDisk(ses, summaryMsgID, lastMsgID, tokenCount); err != nil {
				log.Printf("[chat] checkpoint save failed: %v", err)
			} else {
				log.Printf("[chat] checkpoint saved: summary_msg_id=%s last_msg_id=%s tokens=%d", summaryMsgID, lastMsgID, tokenCount)
			}
		})

		ag.SetPersistHandler(func(msg provider.Message) {
			b.mu.Lock()

			ses := b.persistSession
			mismatch := b.sessionLockMismatchLocked(ses)
			b.mu.Unlock()
			if ses == nil || mismatch {
				return
			}
			b.appendPersistMessage(jsonlStore, ses, msg)
		})
	}

	b.bindTunnelProjectionSession()
	return nil
}

// SetIMManager injects the IM runtime manager into the im tool so the LLM
// can manage adapters (status, mute/unmute, disable/enable, send).
func (b *ChatBridge) SetIMManager(mgr tool.IMManager) {
	if mgr == nil {
		return
	}
	if imt, ok := b.registry.Get("im"); ok {
		if imTool, ok := imt.(tool.IMTool); ok {
			imTool.Manager = mgr
			b.registry.Unregister("im")
			b.registry.Register(imTool)
		}
	}
}

// SetRuntimeStatusProvider injects the runtime status provider into the
// runtime tool so the LLM can query session ID, IM adapters, mobile status.
func (b *ChatBridge) SetRuntimeStatusProvider() {
	if rt, ok := b.registry.Get("runtime"); ok {
		if rTool, ok := rt.(tool.RuntimeTool); ok {
			rTool.Provider = &desktopRuntimeProvider{bridge: b}
			b.registry.Unregister("runtime")
			b.registry.Register(rTool)
		}
	}
}

// desktopRuntimeProvider implements tool.RuntimeStatusProvider for desktop.
type desktopRuntimeProvider struct {
	bridge *ChatBridge
}

func (p *desktopRuntimeProvider) RuntimeSessionID() string {
	p.bridge.mu.Lock()
	defer p.bridge.mu.Unlock()
	if p.bridge.currentSes != nil {
		return p.bridge.currentSes.ID
	}
	return ""
}

func (p *desktopRuntimeProvider) RuntimePermissionMode() string {
	p.bridge.mu.Lock()
	defer p.bridge.mu.Unlock()
	if p.bridge.currentSes != nil && p.bridge.currentSes.PermissionMode != "" {
		return p.bridge.currentSes.PermissionMode
	}

	return agentruntime.InteractivePermissionModeWithDefault(p.bridge.cfg, false, "supervised").String()
}

func (p *desktopRuntimeProvider) RuntimeVendor() string {
	if p.bridge.cfg != nil {
		return p.bridge.cfg.Vendor
	}
	return ""
}

func (p *desktopRuntimeProvider) RuntimeEndpoint() string {
	if p.bridge.cfg != nil {
		return p.bridge.cfg.Endpoint
	}
	return ""
}

func (p *desktopRuntimeProvider) RuntimeModel() string {
	if p.bridge.cfg != nil {
		return p.bridge.cfg.Model
	}
	return ""
}

func (p *desktopRuntimeProvider) RuntimeLanguage() string {
	if p.bridge.cfg != nil {
		return p.bridge.cfg.Language
	}
	return ""
}

func (p *desktopRuntimeProvider) RuntimeContextWindow() int {
	if p.bridge.agent != nil && p.bridge.agent.ContextManager() != nil {
		return p.bridge.agent.ContextManager().ContextWindow()
	}
	return 0
}

func (p *desktopRuntimeProvider) RuntimeMaxTokens() int {
	if p.bridge.agent != nil && p.bridge.agent.ContextManager() != nil {
		return p.bridge.agent.ContextManager().OutputReserve()
	}
	return 0
}

func (p *desktopRuntimeProvider) RuntimeIMAdapters() []tool.RuntimeIMAdapterInfo {

	if rt, ok := p.bridge.registry.Get("im"); ok {
		if imTool, ok := rt.(tool.IMTool); ok && imTool.Manager != nil {
			snap := imTool.Manager.Snapshot()
			channels := make(map[string]string)
			for _, b := range snap.CurrentBindings {
				channels[b.Adapter] = b.ChannelID
			}
			var result []tool.RuntimeIMAdapterInfo
			for _, a := range snap.Adapters {
				result = append(result, tool.RuntimeIMAdapterInfo{
					Name:     a.Name,
					Platform: string(a.Platform),
					Online:   a.Healthy,
					Muted:    a.Status == "muted",
					Channel:  channels[a.Name],
				})
			}
			return result
		}
	}
	return nil
}

func (p *desktopRuntimeProvider) RuntimeMobile() tool.RuntimeMobileInfo {
	p.bridge.mu.Lock()
	defer p.bridge.mu.Unlock()
	var info tool.RuntimeMobileInfo
	if p.bridge.tunnelHost != nil {
		if broker := p.bridge.tunnelHost.OnlineBroker(); broker != nil {
			info.Connected = broker.SessionID() != ""
			info.SessionID = broker.SessionID()
		}
		if shareInfo := p.bridge.tunnelHost.GetShareInfo(); shareInfo != nil {
			info.RelayURL = shareInfo.ConnectURL
			info.ConnectCode = shareInfo.RoomID
		}
	}
	return info
}

func (b *ChatBridge) StartMCPOAuth(ctx context.Context, serverName string, openURL func(string) error) (*MCPOAuthStartResult, error) {
	if b == nil || b.mcpManager == nil {
		return nil, fmt.Errorf("MCP manager not initialized")
	}
	oauthErr := b.mcpManager.PendingOAuthByName(serverName)
	if oauthErr == nil || oauthErr.Handler == nil {
		return nil, fmt.Errorf("MCP server %q is not waiting for OAuth login", serverName)
	}

	handler := oauthErr.Handler
	startCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if handler.SupportsDCR() {
		if err := handler.RegisterClient(startCtx); err != nil {
			debug.Log("mcp-oauth", "desktop_dcr_failed server=%s error=%v, continuing", serverName, err)
		}
	}

	result := &MCPOAuthStartResult{ServerName: serverName}
	if handler.SupportsDeviceFlow() {

		scopes := handler.GetScopes()
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
	oauthErr := b.mcpManager.PendingOAuthByName(serverName)
	if oauthErr == nil || oauthErr.Handler == nil {
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
	b.mcpManager.ClearPendingOAuth(serverName)
	if !b.mcpManager.Retry(serverName) {
		return fmt.Errorf("MCP server %q not found for reconnect", serverName)
	}
	return nil
}

func (b *ChatBridge) subagentPanelTitle(agentID string) string {
	b.mu.Lock()
	mgr := b.subAgentMgr
	b.mu.Unlock()
	if mgr == nil {
		return agentID
	}
	snap, ok := mgr.SnapshotByID(agentID)
	if !ok {
		return agentID
	}

	switch {
	case strings.TrimSpace(snap.Name) != "":
		name := snap.Name
		if strings.TrimSpace(snap.Model) != "" {
			name = name + " [" + strings.TrimSpace(snap.Model) + "]"
		}
		return name
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

	b.mu.Lock()
	push := b.tunnelPush
	th := b.tunnelHost
	b.mu.Unlock()
	if push != nil {
		push(ev)
	} else if th != nil {
		th.PushStreamEvent(ev)
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
	msgs := mergeTunnelUserMessages(
		buildSessionHistoryFromMessages(b.currentSes.Messages),
		b.currentSes.SnapshotTunnelEvents(),
	)
	return msgs
}

// mergeTunnelUserMessages appends tunnel-recorded user messages that are not
// already present in the rendered history. Tunnel events record messages sent
// from mobile/tunnel; without merging they are invisible when a saved session
// is reloaded (#242). Dedup: when a tunnel event carries a message_id, it is
// deduplicated by ID only — multiple distinct tunnel events with the same text
// (e.g. the user sending "yes" twice) are each kept (#268). Exact
// (whitespace-trimmed) text matching is only a fallback for events without an
// ID, guarding against the same tunnel message being replayed after it has
// already been persisted as a session message — conservative: prefer a rare
// duplicate over losing a message.
func mergeTunnelUserMessages(msgs []SessionMessage, tunnelEvents []session.TunnelEvent) []SessionMessage {
	seenIDs := make(map[string]struct{}, len(msgs))
	seenText := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		if m.ID != "" {
			seenIDs[m.ID] = struct{}{}
		}
		if key := strings.TrimSpace(m.Content); key != "" {
			seenText[key] = struct{}{}
		}
	}
	for _, te := range tunnelEvents {
		if te.Type != "user_message" {
			continue
		}
		var data struct {
			Text      string `json:"text"`
			MessageID string `json:"message_id"`
		}
		if json.Unmarshal(te.Data, &data) != nil || data.Text == "" {
			continue
		}
		if data.MessageID != "" {

			if _, dup := seenIDs[data.MessageID]; dup {
				continue
			}
			seenIDs[data.MessageID] = struct{}{}
		} else {

			if _, dup := seenText[strings.TrimSpace(data.Text)]; dup {
				continue
			}
			if key := strings.TrimSpace(data.Text); key != "" {
				seenText[key] = struct{}{}
			}
		}
		msgs = append(msgs, SessionMessage{
			ID:      data.MessageID,
			Role:    "user",
			Content: data.Text,
		})
	}
	return msgs
}

func (b *ChatBridge) appendLiveUserMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {

		b.liveHistory = mergeTunnelUserMessages(
			buildSessionHistoryFromMessages(b.currentSes.Messages),
			b.currentSes.SnapshotTunnelEvents(),
		)
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
		b.liveHistory = mergeTunnelUserMessages(
			buildSessionHistoryFromMessages(b.currentSes.Messages),
			b.currentSes.SnapshotTunnelEvents(),
		)
	}
	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "error",
		Content: text,
	})
}

// appendLiveErrorIfCurrent appends an error entry to the live history only
// when gen is still the current run generation (#550 E1): a superseded
// run's late error used to bypass the emitIfCurrent guard and leak into the
// newly selected session's history.
func (b *ChatBridge) appendLiveErrorIfCurrent(gen uint64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	b.mu.Lock()
	stale := gen != b.runGeneration
	b.mu.Unlock()
	if stale {
		debug.Log("wailskit", "drop stale-run live error (superseded run) (#550 E1)")
		return
	}
	b.appendLiveError(text)
}

func (b *ChatBridge) applySemanticToLiveHistory(semantic agentruntime.DesktopStreamSemantic) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.liveHistory) == 0 && b.currentSes != nil {
		b.liveHistory = mergeTunnelUserMessages(
			buildSessionHistoryFromMessages(b.currentSes.Messages),
			b.currentSes.SnapshotTunnelEvents(),
		)
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

// finalizeStreamingToolsLocked clears the Streaming flag of ALL tool-role
// entries (#3771). The only other clear point is ToolResult matching by
// ToolID; when a run is cancelled/preempted/superseded mid-tool-execution,
// ToolResult never arrives and the tool entry spins forever in the frontend
// until the session is rebuilt. Walk the whole history - parallel tools put
// entries at non-tail positions.
func (b *ChatBridge) finalizeStreamingToolsLocked() {
	for i := range b.liveHistory {
		if b.liveHistory[i].Role == "tool" && b.liveHistory[i].Streaming {
			b.liveHistory[i].Streaming = false
		}
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

// AttachTunnelBroker connects the broker for outbound event push to mobile.
// All negotiation (session_info, replay, status, announce) is handled by
// TunnelHost.PrepareOnlineShare — the canonical share bootstrap.
func (b *ChatBridge) AttachTunnelBroker(broker *tunnel.Broker) {
	var (
		currentSes *session.Session
		cfg        *config.Config
	)
	b.mu.Lock()
	currentSes = b.currentSes
	cfg = b.cfg
	b.mu.Unlock()

	if broker == nil {
		return
	}

	broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		snapshot := tunnel.BrokerSnapshot{}
		if cfg != nil {
			status := b.CurrentTunnelStatus()
			snapshot.Status = status
			activity := b.CurrentTunnelActivity()
			if activity != "" {
				snapshot.Activity = tunnel.ActivityData{Activity: activity}
			}
		}
		return snapshot
	})

	if cfg != nil {
		resolved, _ := cfg.ResolveActiveEndpoint()
		model := ""
		vendorName := ""
		if resolved != nil {
			model = resolved.Model
			vendorName = resolved.VendorName
		}
		b.mu.Lock()
		if th := b.tunnelHost; th != nil {
			// #4495: currentSes is a raw snapshot - it is nil until the first
			// session is created. A phone connecting to the share tunnel on a
			// cold start (no message sent yet) panicked here on the bare
			// .Title deref while holding b.mu. Zero-value the session-derived
			// fields instead; cf. the cur != nil gate before BindSession below
			// and pushTunnelSessionInfo.
			var title string
			if currentSes != nil {
				title = currentSes.Title
			}
			th.SetSessionInfo(tunnel.SessionInfoData{
				Title:     title,
				Workspace: b.workingDir,
				Model:     model,
				Provider:  vendorName,
				Mode:      cfg.DefaultMode,
				Language:  cfg.Language,
			})
		}
		b.mu.Unlock()
	}

	b.mu.Lock()
	th := b.tunnelHost
	store := b.sessionStore
	cur := currentSes
	b.mu.Unlock()
	if th != nil {
		th.AttachOnlineBroker(broker)
		if cur != nil {
			th.BindSession(cur, store)
		}
		th.PrepareOnlineShare(broker)
	}
}

func (b *ChatBridge) DetachTunnelBroker() {
	b.mu.Lock()
	th := b.tunnelHost
	b.mu.Unlock()
	if th != nil {
		th.DetachOnlineBroker()
	}
}

func (b *ChatBridge) currentTunnelBroker() *tunnel.Broker {
	b.mu.Lock()
	th := b.tunnelHost
	b.mu.Unlock()
	if th != nil {
		if pb := th.ProjectionBroker(); pb != nil {
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

	b.mu.Lock()
	busy := b.cancel != nil
	b.mu.Unlock()
	if busy {
		return tunnel.StatusData{Status: tunnel.StatusBusy}
	}
	return tunnel.StatusData{Status: tunnel.StatusIdle}
}

func (b *ChatBridge) CurrentTunnelActivity() string {

	b.mu.Lock()
	processing := b.cancel != nil
	b.mu.Unlock()
	switch {
	case b.interactions != nil && b.interactions.ApprovalCount() > 0:
		return "approval"
	case b.interactions != nil && b.interactions.AskUserCount() > 0:
		return "ask_user"
	case processing:
		return "processing"
	default:
		return ""
	}
}

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
	b.mu.Lock()
	th := b.tunnelHost
	b.mu.Unlock()
	if th != nil {
		return th.AuthorityEpoch()
	}
	return 1
}

func (b *ChatBridge) CurrentSessionTunnelEvents() []tunnel.GatewayMessage {
	b.mu.Lock()
	th := b.tunnelHost
	b.mu.Unlock()
	if th != nil {
		return th.TunnelEvents()
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
	b.mu.Lock()
	th := b.tunnelHost
	ses0 := b.currentSes
	b.mu.Unlock()
	if th == nil {
		return
	}
	store := th.ProjectionStore()
	ses := ses0
	if ses == nil || store == nil {
		return
	}
	_ = store.DeleteSession(ses.ID)
}

func (b *ChatBridge) CurrentTunnelHistory() []tunnel.HistoryEntry {

	return nil
}

// nextInteractionRequestID returns a unique request ID for approval/ask_user
// round-trips. When a tunnel broker is connected its NextMessageID namespace
// is used (mobile replies resolve against it); otherwise a bridge-local
// "req-N" sequence is generated so concurrent desktop-only approvals never
// share the empty-string key (#4151).
func (b *ChatBridge) nextInteractionRequestID() string {
	if broker := b.currentTunnelBroker(); broker != nil {
		return broker.NextMessageID()
	}
	return fmt.Sprintf("req-%d", b.localReqSeq.Add(1))
}

// RequestApproval blocks until the user (desktop or mobile) responds to an
// approval request.  It stores a pending channel, pushes the request to a
// connected tunnel broker, and emits an event so the Wails frontend can show
// an approval dialog.
func (b *ChatBridge) RequestApproval(ctx context.Context, requestID, toolName, input string) permission.Decision {
	req := agentruntime.ApprovalRequest{ID: requestID, ToolName: toolName, Input: input}

	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelApprovalRequest(broker, requestID, toolName, input, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]string{
			"requestID": requestID,
			"toolName":  toolName,
			"input":     input,
		})
		b.OnStreamEvent("approval:request", raw)
	}

	approvalCtx, cancelApproval := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	defer cancelApproval()
	decision := b.interactions.AwaitApproval(approvalCtx, req)
	if approvalCtx.Err() != nil {

		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"requestID": requestID, "reason": "timeout"})
			b.OnStreamEvent("approval:cancel", raw)
		}
		agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), requestID, "timeout", agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}
	return decision
}

// RequestAskUser blocks until the user (desktop or mobile) responds to a
// structured questionnaire.  It mirrors the Fyne handleAskUser flow.
func (b *ChatBridge) RequestAskUser(ctx context.Context, requestID string, req tool.AskUserRequest) (tool.AskUserResponse, error) {
	if len(req.Questions) == 0 {
		return tool.AskUserResponse{Status: tool.AskUserStatusSubmitted}, nil
	}
	request := agentruntime.AskUserRequest{ID: requestID, Request: req}

	if broker := b.currentTunnelBroker(); broker != nil {
		agentruntime.PushTunnelAskUserRequest(broker, requestID, req, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusWaiting,
		})
	}

	if b.OnStreamEvent != nil {
		payload := map[string]interface{}{
			"requestID": requestID,
			"title":     req.Title,
			"questions": req.Questions,
		}
		raw, _ := json.Marshal(payload)
		b.OnStreamEvent("ask_user:request", raw)
	}

	askCtx, cancelAsk := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	defer cancelAsk()
	resp, err := b.interactions.AwaitAskUser(askCtx, request)
	if askCtx.Err() != nil {

		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"requestID": requestID, "reason": "timeout"})
			b.OnStreamEvent("ask_user:cancel", raw)
		}
		agentruntime.PushTunnelAskUserResult(b.currentTunnelBroker(), requestID, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
	}
	return resp, err
}

// RespondApproval delivers a desktop-originated approval decision to the
// waiting channel.  decision is "allow", "deny", or "always_allow".
func (b *ChatBridge) RespondApproval(requestID, decision string) {
	d := agentruntime.ApprovalDecisionFromTunnel(decision)
	req, ok := b.interactions.ResolveApproval(requestID, d)
	if !ok {

		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"requestID": requestID, "reason": "stale"})
			b.OnStreamEvent("approval:cancel", raw)
		}
		agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), requestID, "stale", agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
		return
	}

	b.mu.Lock()
	agentSnapshot := b.agent
	b.mu.Unlock()
	if (decision == "always_allow" || decision == "always") && agentSnapshot != nil {
		if p, ok := agentSnapshot.PermissionPolicy().(*permission.ConfigPolicy); ok {
			if cmd := permission.ExtractCommandFromInput(req.Input); cmd != "" {
				if pattern := permission.CommandPrefixToPattern(cmd); pattern != "" {
					p.AllowCommandPattern(pattern)
				} else {
					p.SetOverride(req.ToolName, permission.Allow)
				}
			} else {
				p.SetOverride(req.ToolName, permission.Allow)
			}
		}
	}

	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), requestID, decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})

}

// PendingApprovalIDs returns the pending approval request IDs in
// registration order (#1657 case 2) - the text bridge uses this to detect
// ambiguous bare "y" replies when several approvals are pending.
func (b *ChatBridge) PendingApprovalIDs() []string {
	reqs := b.interactions.PendingApprovals()
	ids := make([]string, 0, len(reqs))
	for _, r := range reqs {
		ids = append(ids, r.ID)
	}
	return ids
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

		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"requestID": requestID, "reason": "stale"})
			b.OnStreamEvent("ask_user:cancel", raw)
		}
		agentruntime.PushTunnelAskUserResult(b.currentTunnelBroker(), requestID, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
		return
	}

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

		agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), data.ID, "stale", agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
		return
	}
	agentruntime.ResolveTunnelApproval(data.Decision, req.ToolName, func(toolName string) {

		b.mu.Lock()
		agentSnapshot := b.agent
		b.mu.Unlock()
		if agentSnapshot == nil {
			return
		}
		p, ok := agentSnapshot.PermissionPolicy().(*permission.ConfigPolicy)
		if !ok {
			return
		}

		if cmd := permission.ExtractCommandFromInput(req.Input); cmd != "" {
			if pattern := permission.CommandPrefixToPattern(cmd); pattern != "" {
				p.AllowCommandPattern(pattern)
				return
			}
		}

		p.SetOverride(toolName, permission.Allow)
	})

	agentruntime.PushTunnelApprovalResult(b.currentTunnelBroker(), data.ID, data.Decision, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// HandleMobileAskUserResponse processes an ask_user response received from
// the mobile client via the tunnel.
func (b *ChatBridge) HandleMobileAskUserResponse(data tunnel.AskUserResponseData, _ tool.AskUserRequest) {

	req, found := b.interactions.PendingAskUser(data.ID)
	if !found {
		return
	}
	response := agentruntime.BuildAskUserResponseFromTunnel(req.Request, data.Status, data.Answers)
	if _, ok := b.interactions.ResolveAskUser(data.ID, response); !ok {

		agentruntime.PushTunnelAskUserResult(b.currentTunnelBroker(), data.ID, agentruntime.TunnelStateUpdate{
			HasStatus: true,
			Status:    tunnel.StatusBusy,
		})
		return
	}

	agentruntime.PushTunnelAskUserResponse(b.currentTunnelBroker(), data.ID, response, agentruntime.TunnelStateUpdate{
		HasStatus: true,
		Status:    tunnel.StatusBusy,
	})
}

// CurrentAskUserRequest returns the pending ask_user request for the given ID,
// or nil if none exists.  Used by HandleMobileAskUserResponse to reconstruct
// the full response with completion metadata.
func (b *ChatBridge) CurrentAskUserRequest(requestID string) tool.AskUserRequest {

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

// recordSessionUsage accumulates token usage into the session.
// Mirrors Fyne AgentBridge.recordSessionUsage and TUI Model.recordSessionUsage exactly.
func (b *ChatBridge) recordSessionUsage(usage provider.TokenUsage, source string) {
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
		Source:    source,
	}
	b.mu.Unlock()

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendMetaToDisk(ses)
		_ = jsonlStore.AppendUsageEntry(ses, entry)
	} else {
		_ = store.Save(ses)
	}

	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(b.currentUsagePayload())
		b.OnStreamEvent("usage_update", raw)
	}
}

// desktopResolveRate mirrors internal/tui resolveRate (commands_slash_info.go):
// explicit pricing table entry first, then coding-plan endpoint, then
// whole-vendor subscription, else unknown (metered with no known rate).
func desktopResolveRate(vendor, endpoint, model string) cost.ModelRate {
	pt := cost.DefaultPricingTable()
	if rate, ok := pt.Get(vendor, model); ok {
		return rate
	}
	if cost.IsCodingPlanEndpoint(endpoint) {
		return cost.ModelRate{Type: cost.PricingSubscription, Plan: "Coding Plan"}
	}
	if plan := cost.IsSubscriptionVendor(vendor); plan != "" {
		return cost.ModelRate{Type: cost.PricingSubscription, Plan: plan}
	}
	return cost.ModelRate{}
}

func (b *ChatBridge) currentUsagePayload() map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	payload := map[string]interface{}{
		"inputTokens":             0,
		"outputTokens":            0,
		"cacheRead":               0,
		"cacheWrite":              0,
		"cacheHit":                0,
		"contextUsed":             0,
		"contextTotal":            0,
		"usagePercent":            0,
		"compactRemainingPercent": 0,
		"pricingType":             "",
		"planLabel":               "",
	}
	if b.currentSes != nil {
		usage := b.currentSes.UsageForEndpoint(b.currentSes.Vendor, b.currentSes.Endpoint)
		payload["inputTokens"] = usage.DisplayInputTokens()
		payload["outputTokens"] = usage.OutputTokens
		payload["cacheRead"] = usage.CacheRead
		payload["cacheWrite"] = usage.CacheWrite
		payload["cacheHit"] = usage.CacheHitPercent()

		rate := desktopResolveRate(b.currentSes.Vendor, b.currentSes.Endpoint, b.currentSes.Model)
		payload["pricingType"] = string(rate.Type)
		payload["planLabel"] = rate.Plan
	}
	if b.agent != nil {
		cm := b.agent.ContextManager()
		display, ok := uiusage.BuildContextDisplay(cm.TokenCount(), cm.ContextWindow(), cm.AutoCompactThreshold())
		if ok {
			payload["contextUsed"] = display.UsedTokens
			payload["contextTotal"] = display.MaxTokens
			payload["usagePercent"] = display.UsagePercent

			payload["compactRemainingPercent"] = display.RemainingPercent
		}
	}
	return payload
}

// recordMetric stores a metric event for turn digest generation.
// skillOnMetric forwards skill sub-agent telemetry to the shared collector
// (#3296); events arrive pre-stamped with the sub-agent's own model.
func (b *ChatBridge) skillOnMetric(ev metrics.MetricEvent) {
	if b.metricCollector != nil {
		b.metricCollector.Emit(ev)
	}
}

func (b *ChatBridge) recordMetric(ev interface{}) {
	me, ok := ev.(metrics.MetricEvent)
	if !ok {
		return
	}
	b.mu.Lock()
	me.TurnIndex = b.usageTurnIndex
	if b.currentSes != nil {

		if me.Model == "" {
			me.Model = b.currentSes.Model
			me.Vendor = b.currentSes.Vendor
			me.Endpoint = b.currentSes.Endpoint
		}
		b.currentSes.AppendMetricEvent(me)

		b.currentSes.AppendMetricForEndpoint(me.Vendor, me.Endpoint, me)
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

	b.liveHistory = append(b.liveHistory, SessionMessage{
		Role:    "system",
		Content: text,
	})
	b.mu.Unlock()

	if b.OnStreamEvent != nil {
		raw, _ := json.Marshal(map[string]string{
			"type": "system",
			"text": text,
		})
		b.OnStreamEvent("system", raw)
	}
}

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
	ses := b.currentSes
	store := b.sessionStore
	b.mu.Unlock()
	if agent != nil {

		if cur := agent.PermissionPolicy(); cur != nil {
			if ms, ok := cur.(interface {
				SetMode(permission.PermissionMode)
			}); ok {
				ms.SetMode(mode)
			} else {
				agent.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode))
			}
		} else {
			agent.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{b.workingDir}, mode))
		}

		b.refreshSystemPrompt()
	}

	if ses != nil && store != nil {
		ses.PermissionMode = modeStr
		_ = store.AppendMetaToDisk(ses)
	}
}

// desktopModeSwitcher implements tool.ModeSwitcher for the Wails desktop app.
// It bridges LLM-initiated mode changes (via switch_mode tool) to the
// ChatBridge's SetPermissionMode, which updates the agent policy and UI.
type desktopModeSwitcher struct {
	bridge *ChatBridge
}

func (d *desktopModeSwitcher) Mode() permission.PermissionMode {
	return d.bridge.permissionMode
}

func (d *desktopModeSwitcher) SetMode(mode permission.PermissionMode) {
	d.bridge.SetPermissionMode(mode.String())
}

func (d *desktopModeSwitcher) RememberMode(mode permission.PermissionMode) permission.PermissionMode {

	return d.bridge.permissionMode
}

func (d *desktopModeSwitcher) RestoreMode(fallback permission.PermissionMode) permission.PermissionMode {
	return fallback
}

// QueueMessage stores a user message to be sent after the current agent turn.
func (b *ChatBridge) QueueMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, false, nil)

	b.mu.Lock()
	b.pendingSource = append(b.pendingSource, "desktop")
	b.pendingExclude = append(b.pendingExclude, "")
	b.mu.Unlock()
}

// QueueHiddenMessage stores a hidden message (mirrors Fyne).
func (b *ChatBridge) QueueHiddenMessage(msg string) {
	b.pendingMsgs.Enqueue(msg, true, nil)
}

func (b *ChatBridge) drainPending() (agentruntime.PendingMessage[*tunnel.MessageData], bool) {
	return b.pendingMsgs.Consume()
}

func (b *ChatBridge) drainPendingInterrupt() []provider.ContentBlock {
	pending, ok := b.drainPending()
	if !ok {
		return nil
	}

	if !pending.Hidden {
		b.mu.Lock()
		if len(b.pendingSource) > 0 {
			b.pendingSource = b.pendingSource[1:]
		}
		if len(b.pendingExclude) > 0 {
			b.pendingExclude = b.pendingExclude[1:]
		}
		b.mu.Unlock()
	}
	if b.OnStreamEvent != nil {
		b.OnStreamEvent("pending_consumed", nil)
	}
	if !pending.Hidden {

	}

	return []provider.ContentBlock{{Type: "text", Text: strings.TrimSpace(pending.Text)}}
}

// SendHiddenText sends a hidden message to the agent without UI display.
// SendHiddenText injects text into the agent loop without rendering it as a
// visible user message in the chat. Used for LAN Chat agent-to-agent messages
// where the incoming text is redundant (the agent's response is what matters).
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
	b.finished = false
	b.persistedRunCount = 0
	b.usageTurnIndex++
	b.startTime = time.Now()

	b.runGeneration++
	b.activeRunGen = b.runGeneration

	b.startDesktopTurnLocked()
	b.runSes = b.currentSes
	b.mu.Unlock()

	if b.lanchatHub != nil {
		b.lanchatHub.SetAgentBusy(true)
	}

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
				if broker := b.currentTunnelBroker(); broker != nil {
					broker.PushSystemMessage("Processing queued message...")
				}
				data := tunnel.MessageData{Text: pending.Text}
				src := "desktop"
				exclude := ""
				if pending.Meta != nil {
					data = *pending.Meta
					src = "mobile"
				}

				b.mu.Lock()
				if len(b.pendingSource) > 0 {
					src = b.pendingSource[0]
					b.pendingSource = b.pendingSource[1:]
				}
				if len(b.pendingExclude) > 0 {
					exclude = b.pendingExclude[0]
					b.pendingExclude = b.pendingExclude[1:]
				}
				b.mu.Unlock()
				_ = b.sendMessageData(data, src, exclude)
			}
		}
	}()

	if b.agent == nil {

		b.mu.Lock()
		ses := b.currentSes
		mismatch := b.sessionLockMismatchLocked(ses)
		b.mu.Unlock()
		if mismatch {
			err := fmt.Errorf("session %s lock mismatch; refusing agent auto-rebuild", ses.ID)
			b.finishRun(err)
			return err
		}
		if err := b.InitAgent(ctx); err != nil {
			b.finishRun(err)
			return fmt.Errorf("init agent: %w", err)
		}
	}

	runGen := b.currentRunGeneration()

	b.mu.Lock()
	b.runSes = b.currentSes
	b.mu.Unlock()

	b.mu.Lock()
	if b.activeRunGen == runGen {
		b.persistSession = b.runSes
	}
	b.mu.Unlock()
	err := b.agent.RunStream(ctx, text, func(ev provider.StreamEvent) {
		b.emitIfCurrent(runGen, ev)
	})
	b.finishRun(err)
	return err
}

// Close cleans up all resources (mirrors Fyne AgentBridge.Close).
// startA2A starts the A2A server, registers this instance, and wires the
// remote tool so the agent can discover and delegate to other ggcode instances.
func (b *ChatBridge) startA2A(cfg *config.Config, ag *agent.Agent, reg *tool.Registry) {

	b.stopA2A()

	if os.Getenv("GGCODE_WAILSKIT_DISABLE_A2A") == "1" {
		return
	}

	if cfg.A2A.Disabled {
		return
	}

	a2aReg, err := a2a.NewRegistry()
	if err != nil {
		log.Printf("[a2a] failed to create registry: %v", err)
		return
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
		Delegation: a2a.DelegationPolicy{
			RequireOnBehalfOf: cfg.A2A.Delegation.RequireOnBehalfOf,
			MaxDepth:          cfg.A2A.Delegation.MaxDepth,
		},
	}, handler)

	if err := srv.Start(); err != nil {
		log.Printf("[a2a] failed to start server: %v", err)
		return
	}

	instance := a2a.InstanceInfo{
		ID:           a2a.GenerateInstanceID(),
		PID:          os.Getpid(),
		Workspace:    b.workingDir,
		StartedAt:    time.Now().Format(time.RFC3339),
		Endpoint:     srv.Endpoint(),
		AgentCardURL: srv.Endpoint() + "/.well-known/agent.json",
		Status:       "ready",
	}
	a2aReg.SetInterfaces(cfg.A2A.Interfaces)
	if err := a2aReg.Register(instance); err != nil {
		log.Printf("[a2a] failed to register: %v", err)
		srv.Stop()
		return
	}

	apiKey := cfg.A2A.EffectiveAPIKey()
	remoteTool := a2a.NewRemoteTool(a2aReg, apiKey)
	_ = reg.Register(remoteTool)

	bridgeClient := a2a.NewClient(srv.Endpoint(), apiKey)
	for _, t := range a2a.MCPBridgeTools(bridgeClient) {
		_ = reg.Register(t)
	}

	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	a2aReg.StartBackgroundRefresh(refreshCtx)

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

	chatStore := lanchat.NewStore(filepath.Join(config.ConfigDir(), "lanchat"))
	b.lanchatHub = lanchat.NewHub(
		a2aReg.SelfID(),
		"gui",
		srv.Endpoint(),
		cfg.LanChat.EffectiveAPIKey(),
		chatStore,
		lanchat.DetectWorkspaceMeta(b.workingDir),
	)
	b.lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
	lanchat.MountHandlers(srv.Mux(), b.lanchatHub, srv.Port())

	b.lanchatHub.SetHealthProber(func(ctx context.Context) error {
		if b.agent == nil {
			return fmt.Errorf("agent not ready")
		}
		return b.agent.HealthCheck(ctx)
	})
	b.lanchatHub.SetModel(b.cfg.Model)

	b.registry.Register(tool.NewLanChatTool(b.lanchatHub, b.cfg.LanChat))

	b.lanchatHub.SetCallbacks(
		func(msg lanchat.Message) {

			if msg.IsDirectToAgent() && b.lanchatHub != nil && msg.ToNodeID == b.lanchatHub.NodeID() {
				return
			}
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:message", msg)
			}
		},
		func(r lanchat.Receipt) {

			if r.FromRole == lanchat.RoleAgent {
				return
			}
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:receipt", r)
			}
		},
		func(p lanchat.Participant) {
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:participant_added", p)
			}
		},
		func(nodeID, humanNick string) {
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:participant_removed", map[string]string{"node_id": nodeID, "nick": humanNick})
			}
		},
		func(ap lanchat.PendingAgentMsg) {
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:approval_request", ap)
			}
		},
		func(nodeID, oldNick, newNick string) {
			if b.EmitEvent != nil {
				b.EmitEvent("lanchat:nick_change", map[string]string{"node_id": nodeID, "old_nick": oldNick, "new_nick": newNick})
			}
		},
	)

	b.lanchatHub.SetOnAutoApprove(func(msg lanchat.Message) {
		agentText := fmt.Sprintf("[LAN Chat from %s]: %s", msg.FromNick, msg.Content)

		if b.OnStreamEvent != nil {
			raw, _ := json.Marshal(map[string]string{"text": agentText, "source": "lanchat"})
			b.OnStreamEvent("user_message", raw)
		}
		_ = b.SendHiddenText(agentText)
	})

	safego.Go("desktop.a2a-peer-sync", func() {

		select {
		case <-time.After(3 * time.Second):
			b.syncLanChatPeers()
		case <-refreshCtx.Done():
			return
		}

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if b.a2aRegistry == nil {
					return
				}
				b.syncLanChatPeers()
			case <-refreshCtx.Done():
				return
			}
		}
	})

	log.Printf("[a2a] server started at %s", srv.Endpoint())

	if b.agent != nil && b.lanchatHub != nil {
		hubCopy := b.lanchatHub
		wd := b.workingDir
		b.agent.SetSystemPromptInjector(func() string {
			return lanchat.FormatPeersInfo(hubCopy, wd)
		})
	}
}

// startOAuthRenewalWatcher silently renews an expired Anthropic OAuth token
// (#1805 case 2): the running provider froze the access token at build time,
// so a session that outlives expires_in 401'd on every message until restart
// or a config change - despite a perfectly good refresh token. Once a minute,
// when the stored token is OAuth+expired+refreshable, nudge
// OnConfigProviderChanged: its resolve path performs the refresh (and the
// #1805 case-1 cleanup when the refresh token is permanently dead).
func (b *ChatBridge) startOAuthRenewalWatcher() {
	if b.oauthRenewCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.oauthRenewCancel = cancel
	safego.Go("desktop.oauth-renew-watcher", func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if !anthropicOAuthNeedsRenewal() {
					continue
				}
				if bridge := GetChatBridge(); bridge != nil {
					bridge.OnConfigProviderChanged()
				}
			case <-ctx.Done():
				return
			}
		}
	})
}

// anthropicOAuthNeedsRenewal reports whether the stored Anthropic credential
// is an OAuth token that is expired but refreshable (disk check only - the
// actual refresh happens in the resolve path).
func anthropicOAuthNeedsRenewal() bool {
	info, err := auth.DefaultStore().Load(auth.ProviderAnthropic)
	if err != nil || info == nil {
		return false
	}
	return info.Type == "oauth" &&
		info.IsExpired() &&
		strings.TrimSpace(info.RefreshToken) != ""
}

// stopA2A shuts down the A2A server and cleans up.
func (b *ChatBridge) stopA2A() {
	if b.oauthRenewCancel != nil {
		b.oauthRenewCancel()
		b.oauthRenewCancel = nil
	}
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

	if b.registry != nil {
		if b.a2aRemoteTool != nil {
			b.registry.Unregister(b.a2aRemoteTool.Name())
			b.a2aRemoteTool = nil
		}

		for _, name := range []string{"a2a_discover", "a2a_send_task", "a2a_get_task", "a2a_list_tasks", "a2a_cancel_task"} {
			b.registry.Unregister(name)
		}

		b.registry.Unregister("lanchat")
	}
}

// syncLanChatPeers pulls A2A registry instances and pushes them to the lanchat hub.
func (b *ChatBridge) syncLanChatPeers() {
	if b.a2aRegistry == nil || b.lanchatHub == nil {
		return
	}
	instances := b.a2aRegistry.CachedInstances()
	if instances == nil {
		return
	}
	peers := make([]lanchat.Participant, 0, len(instances))
	for _, inst := range instances {
		peers = append(peers, lanchat.Participant{
			NodeID:   inst.ID,
			Endpoint: inst.Endpoint,
		})
	}
	b.lanchatHub.UpdatePeers(peers)
}

func parseA2ATimeout(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {

		fmt.Fprintf(os.Stderr, "warning: invalid a2a.task_timeout %q (%v), using default %v\n", s, err, 5*time.Minute)
		return 5 * time.Minute
	}
	return d
}

func (b *ChatBridge) Close() {

	agentruntime.StopGlobalSectionCollector()

	b.mu.Lock()
	subAgents := b.subAgentMgr
	swarms := b.swarmMgr
	b.mu.Unlock()
	if subAgents != nil {
		subAgents.CancelAll()
	}
	if swarms != nil {
		swarms.CancelAll()
	}

	b.cleanupEphemeralSession()

	b.mu.Lock()
	ses := b.currentSes
	store := b.sessionStore
	b.mu.Unlock()
	if ses != nil && store != nil {
		if jsonlStore, ok := store.(*session.JSONLStore); ok {
			wasDeleted := jsonlStore.WillCleanupIfEmpty(ses)
			if err := jsonlStore.CleanupIfEmpty(ses); err != nil {
				log.Printf("[chat] Close: cleanup empty session failed: %v", err)
			} else if wasDeleted {

				im.ClearSessionBindingsGlobal(ses.ID)
			}
		}
	}

	b.mu.Lock()
	if b.sessionLock != nil {
		b.sessionLock.Release()
		b.sessionLock = nil
	}
	b.mu.Unlock()

	b.stopA2A()

	b.mu.Lock()
	hub := b.lanchatHub
	b.mu.Unlock()
	if hub != nil {
		hub.Close()
	}

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
	b.resolved = resolved
	ses := b.currentSes
	b.mu.Unlock()
	agentruntime.ApplyProviderToAgent(a, prov, resolved)

	if ses != nil {
		ses.Vendor = b.cfg.Vendor
		ses.Endpoint = b.cfg.Endpoint
		ses.Model = resolved.Model
		if b.sessionStore != nil {
			_ = b.sessionStore.AppendMetaToDisk(ses)
		}
	}

	if b.cfg.Vendor != "" && b.cfg.Endpoint != "" {
		agentruntime.SyncVendorEndpointToGlobal(b.cfg, b.cfg.Vendor, b.cfg.Endpoint)
	}

	sessionCW2, sessionMT2 := 0, 0
	if ses != nil {
		sessionCW2 = ses.ContextWindow
		sessionMT2 = ses.MaxTokens
	}
	agentruntime.StartAsyncRelayModelLimitRefreshWithSession(b.cfg, resolved, a, sessionCW2, sessionMT2, func(resp relaycatalog.ResolveResponse) {
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

	if b.lanchatHub != nil {
		b.lanchatHub.SetModel(resolved.Model)
	}
	b.mu.Lock()
	b.resolved = resolved
	if b.currentSes != nil {
		b.currentSes.Vendor = b.cfg.Vendor
		b.currentSes.Endpoint = b.cfg.Endpoint
		b.currentSes.Model = resolved.Model

		if b.sessionStore != nil {
			_ = b.sessionStore.AppendMetaToDisk(b.currentSes)
		}
	}
	b.mu.Unlock()

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

	if b.EmitEvent != nil {
		b.EmitEvent("config:updated", nil)
	}
}

// RefreshEndpointLimits re-resolves the active endpoint and updates the
// running agent's ContextManager so that context_window / max_tokens
// changes take effect immediately without requiring a session restart.
// Session-level overrides (from SetSessionLimits) take priority over
// endpoint/per-model config.
func (b *ChatBridge) RefreshEndpointLimits() {
	if b.cfg == nil {
		return
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return
	}
	b.mu.Lock()
	b.resolved = resolved
	agent := b.agent
	ses := b.currentSes
	b.mu.Unlock()

	if agent != nil {

		ctxWin := 0
		maxTok := 0
		if ses != nil {
			ctxWin = ses.ContextWindow
			maxTok = ses.MaxTokens
		}
		if ctxWin == 0 {
			ctxWin = resolved.ContextWindow
		}
		if maxTok == 0 {
			maxTok = resolved.MaxTokens
		}
		if ctxWin > 0 {
			agent.ContextManager().SetContextWindow(ctxWin)
		}
		if maxTok > 0 {
			agent.ContextManager().SetOutputReserve(maxTok)
		}
	}

	if b.EmitEvent != nil {
		b.EmitEvent("config:updated", nil)
	}
}

// GetModelLimits returns all per-model limit overrides for the active endpoint.
func (b *ChatBridge) GetModelLimits() []ModelLimitInfo {
	if b.cfg == nil {
		return nil
	}
	resolved, err := b.cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil
	}
	return GetModelLimits(resolved.VendorID, resolved.EndpointName)
}

// SessionLimitInfo represents the current session's context_window and max_tokens.
type SessionLimitInfo struct {
	ContextWindow int `json:"contextWindow"`
	MaxTokens     int `json:"maxTokens"`
}

// GetSessionLimits returns the current session's context_window and max_tokens.
// Returns zeros if no session is active or no session-level overrides are set.
func (b *ChatBridge) GetSessionLimits() SessionLimitInfo {
	b.mu.Lock()
	ses := b.currentSes
	b.mu.Unlock()
	if ses == nil {
		return SessionLimitInfo{}
	}
	return SessionLimitInfo{
		ContextWindow: ses.ContextWindow,
		MaxTokens:     ses.MaxTokens,
	}
}

// SetSessionLimits updates the current session's context_window and max_tokens.
// These take priority over endpoint/per-model config.
// A value of 0 means "auto" (falls back to endpoint/per-model config).
// Changes are persisted to the session JSONL and applied to the running agent immediately.
func (b *ChatBridge) SetSessionLimits(contextWindow, maxTokens int) error {
	b.mu.Lock()
	ses := b.currentSes
	store := b.sessionStore
	agent := b.agent
	b.mu.Unlock()

	if ses == nil {
		return fmt.Errorf("no active session")
	}

	ses.ContextWindow = contextWindow
	ses.MaxTokens = maxTokens

	if store != nil {
		if err := store.AppendMetaToDisk(ses); err != nil {
			return fmt.Errorf("failed to persist session limits: %w", err)
		}
	}

	if agent != nil {

		cw, mt := contextWindow, maxTokens
		if cw == 0 || mt == 0 {
			b.mu.Lock()
			resolved := b.resolved
			b.mu.Unlock()
			if resolved != nil {
				if cw == 0 {
					cw = resolved.ContextWindow
				}
				if mt == 0 {
					mt = resolved.MaxTokens
				}
			}
		}
		if cw > 0 {
			agent.ContextManager().SetContextWindow(cw)
		}
		if mt > 0 {
			agent.ContextManager().SetOutputReserve(mt)
		}
	}

	if b.EmitEvent != nil {
		b.EmitEvent("config:updated", nil)
	}

	return nil
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

	restoreVision := b.beginVisionTurnIfNeeded(content)
	defer restoreVision()
	b.mu.Lock()
	if b.cancel != nil {
		b.mu.Unlock()
		return fmt.Errorf("agent is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.cancelled = false
	b.finished = false
	b.persistedRunCount = 0
	b.usageTurnIndex++
	b.startDesktopTurnLocked()
	b.startTime = time.Now()
	b.runSes = b.currentSes

	b.runGeneration++
	b.activeRunGen = b.runGeneration
	b.persistSession = b.currentSes
	runGen := b.runGeneration
	b.mu.Unlock()

	if b.lanchatHub != nil {
		b.lanchatHub.SetAgentBusy(true)
	}

	defer func() {

		restoreVision()

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

				data := tunnel.MessageData{Text: pending.Text}
				src := "desktop"
				exclude := ""
				if pending.Meta != nil {
					data = *pending.Meta
					src = "mobile"
				}
				b.mu.Lock()
				if len(b.pendingSource) > 0 {
					src = b.pendingSource[0]
					b.pendingSource = b.pendingSource[1:]
				}
				if len(b.pendingExclude) > 0 {
					exclude = b.pendingExclude[0]
					b.pendingExclude = b.pendingExclude[1:]
				}
				b.mu.Unlock()
				_ = b.sendMessageData(data, src, exclude)
			}
		}
	}()

	if b.agent == nil {

		b.mu.Lock()
		ses := b.currentSes
		mismatch := b.sessionLockMismatchLocked(ses)
		b.mu.Unlock()
		if mismatch {
			err := fmt.Errorf("session %s lock mismatch; refusing agent auto-rebuild", ses.ID)
			b.finishRun(err)
			return err
		}
		if err := b.InitAgent(ctx); err != nil {
			b.finishRun(err)
			return fmt.Errorf("init agent: %w", err)
		}
	}

	if err := b.ensureSession(); err != nil {
		b.finishRun(err)
		return fmt.Errorf("ensure session: %w", err)
	}

	b.mu.Lock()
	b.runSes = b.currentSes

	if b.activeRunGen == runGen {
		b.persistSession = b.currentSes
	}
	b.mu.Unlock()

	b.bindTunnelProjectionSession()
	if summary := sendContentUserSummary(content); summary != "" {
		b.appendLiveUserMessage(summary)
		if broker := b.currentTunnelBroker(); broker != nil {
			broker.PushUserMessageData(tunnel.MessageData{
				Text:      summary,
				MessageID: broker.NextMessageID(),
			})
			broker.PushStatus(tunnel.StatusBusy, "")
			b.resetTunnelRoundState()
		}
	}

	if b.currentSes != nil {
		msg := provider.Message{Role: "user", Content: content}
		b.currentSes.Messages = append(b.currentSes.Messages, msg)
		b.currentSes.UpdatedAt = time.Now()

	}

	err := b.agent.RunStreamWithContent(ctx, content, func(ev provider.StreamEvent) {
		b.emitIfCurrent(runGen, ev)
	})

	if err != nil && restoreVision != nil && provider.IsImageBlockFallbackCandidate(err) && contentHasImageBlocks(content) {
		debug.Log("chat", "vision turn model rejected image content, retrying text-only: %v", err)
		restoreVision()
		textOnly := stripImageBlocks(content)
		if len(textOnly) == 0 {

			textOnly = []provider.ContentBlock{provider.TextBlock("[image omitted: the active model has no vision support]")}
		}
		{

			b.agent.RemoveLastUserMessage()

			b.persistUserMessageRemoval()
			retryErr := b.agent.RunStreamWithContent(ctx, textOnly, func(ev provider.StreamEvent) {
				b.emitIfCurrent(runGen, ev)
			})
			b.finishRun(retryErr)
			return retryErr
		}
	}
	b.finishRun(err)
	return err
}

// sessionSelectionMatches reports whether the provider InitAgent resolved is
// already the session's saved selection, so LoadSession can skip the
// re-activation (#4123). The comparison is the full (vendor, endpoint, model)
// triple, not the bare model name: model names are free-form strings, and
// identical names across vendors (a gateway re-exporting an official model is
// common) made the old model-name-only short-circuit silently keep the global
// provider while currentSes.Vendor still pointed at the session's vendor —
// divergent state plus usage billed to the wrong endpoint bucket
// (AddUsageForEndpoint bills by ses.Vendor). Sessions saved before
// vendor/endpoint were recorded (both empty) keep the legacy model-name-only
// comparison: ActivateCurrentSelection treats an empty pair as "inherit the
// current selection", so a name match there is a true no-op.
func sessionSelectionMatches(resolved *config.ResolvedEndpoint, sesVendor, sesEndpoint, sesModel string) bool {
	if resolved == nil || resolved.Model != sesModel {
		return false
	}
	if sesVendor == "" && sesEndpoint == "" {

		return true
	}
	return resolved.VendorID == sesVendor && resolved.EndpointID == sesEndpoint
}

// contentHasImageBlocks reports whether any block is an image.
func contentHasImageBlocks(content []provider.ContentBlock) bool {
	for _, c := range content {
		if c.Type == "image" {
			return true
		}
	}
	return false
}

// sendContentUserSummary flattens pasted content blocks into the short user
// turn summary used for liveHistory and the mobile tunnel push (#4124).
// Image-only paste yields "[image]" so the round still shows a user bubble;
// text plus images appends the marker so the bubble reflects the attachment.
func sendContentUserSummary(blocks []provider.ContentBlock) string {
	var text strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" && strings.TrimSpace(blk.Text) != "" {
			if text.Len() > 0 {
				text.WriteByte(' ')
			}
			text.WriteString(strings.TrimSpace(blk.Text))
		}
	}
	summary := text.String()
	if contentHasImageBlocks(blocks) {
		if summary != "" {
			summary += " "
		}
		summary += "[image]"
	}
	return summary
}

// stripImageBlocks removes image blocks, keeping text and other blocks.
func stripImageBlocks(content []provider.ContentBlock) []provider.ContentBlock {
	out := make([]provider.ContentBlock, 0, len(content))
	for _, c := range content {
		if c.Type != "image" {
			out = append(out, c)
		}
	}
	return out
}

// beginVisionTurnIfNeeded performs the turn-scoped vision switch for a
// content-carrying run. Returns a restore func (idempotent; no-op when no
// switch happened).
func (b *ChatBridge) beginVisionTurnIfNeeded(content []provider.ContentBlock) func() {
	if !contentHasImageBlocks(content) {
		return func() {}
	}
	b.mu.Lock()
	cfg := b.cfg
	ag := b.agent
	b.mu.Unlock()
	if cfg == nil || ag == nil || ag.SupportsVision() {
		return func() {}
	}
	vm := agentruntime.VisionTurnModel(cfg)
	if vm == "" {
		return func() {}
	}
	userModel := cfg.Model
	debug.Log("chat", "vision fallback: turn-scoped switch to %s (user model: %s)", vm, userModel)
	resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, "", "", vm)
	if err != nil {

		if r, p, rerr := agentruntime.ActivateCurrentSelection(cfg, "", "", userModel); rerr == nil {
			agentruntime.ApplyProviderToAgent(ag, p, r)
			b.mu.Lock()
			b.resolved = r
			b.mu.Unlock()
		} else {
			debug.Log("chat", "vision fallback switch failed and restore failed: %v / %v", err, rerr)
		}
		debug.Log("chat", "vision fallback switch failed: %v", err)
		return func() {}
	}
	agentruntime.ApplyProviderToAgent(ag, prov, resolved)
	b.mu.Lock()
	b.resolved = resolved
	b.mu.Unlock()
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		if cfg.Model == userModel {
			return
		}
		if r, p, err := agentruntime.ActivateCurrentSelection(cfg, "", "", userModel); err == nil {
			agentruntime.ApplyProviderToAgent(ag, p, r)
			b.mu.Lock()
			b.resolved = r
			b.mu.Unlock()

			if err := cfg.Save(); err != nil {
				debug.Log("chat", "vision fallback: persisting restored model failed: %v", err)
			}
		} else {
			debug.Log("chat", "vision fallback restore failed: %v", err)
		}
	}
}

// GetAvailableModels returns the list of models available for the current endpoint.
func (b *ChatBridge) GetAvailableModels() []string {
	b.mu.Lock()
	resolved := b.resolved
	cfg := b.cfg
	b.mu.Unlock()

	if resolved != nil && len(resolved.Models) > 0 {
		return resolved.Models
	}

	if cfg != nil {
		if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
				if len(ep.Models) > 0 {
					return ep.Models
				}
			}
		}

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
	startupAssets := agentruntime.LoadInteractiveStartupAssets(b.workingDir, autoMem)
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
