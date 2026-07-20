package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// TunnelHost provides unified tunnel stream management for all frontends.
// It owns the projection broker (for recording and replay) and optionally
// forwards events to an online broker (when a mobile Share is active).
type TunnelHost struct {
	mu sync.Mutex

	// Projection layer (always active, even before Share)
	projBroker *tunnel.Broker
	projStore  *tunnel.ProjectionStore
	projBroken bool

	// Online layer (set when a Share is active)
	onlineBroker *tunnel.Broker

	// Stream state
	currentMsgID  string
	needsFinalize bool

	// Session reference for recording
	session      *session.Session
	sessionStore session.Store

	// SessionInfo cached by the frontend for inclusion in active_session.
	sessionInfo tunnel.SessionInfoData

	// Active share session reference (set by StartShare, cleared by StopShare).
	activeShare *tunnelSessionRef
}

// NewTunnelHost creates a new TunnelHost with an offline projection broker.
func NewTunnelHost() *TunnelHost {
	return &TunnelHost{
		projBroker: tunnel.NewBroker(nil), // offline broker, no relay
	}
}

// BindSession binds the tunnel host to a session for event recording.
// Call this when the session changes or is first created.
// Returns the projection broker state with replay events and authority epoch.
func (h *TunnelHost) BindSession(ses *session.Session, store session.Store) ProjectionBrokerState {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.session = ses
	h.sessionStore = store
	h.currentMsgID = ""
	h.needsFinalize = false

	if ses == nil || strings.TrimSpace(ses.ID) == "" {
		return ProjectionBrokerState{}
	}

	// Ensure projection store exists
	if h.projStore == nil {
		if s, err := tunnel.NewDefaultProjectionStore(); err == nil {
			h.projStore = s
		} else {
			h.projBroken = true
		}
	}

	h.projBroken = false

	// Prepare projection broker with event recorder
	state, _ := PrepareProjectionBroker(h.projBroker, h.projStore, ses, func(ev tunnel.GatewayMessage) {
		h.recordEvent(ev)
	})
	return state
}

// ShareConfig holds frontend-specific inputs for starting a mobile share.
type ShareConfig struct {
	Workspace string // absolute working directory
	Model     string
	Provider  string
	Mode      string
	Version   string
	Language  string

	// SnapshotProvider supplies the current chat snapshot for the "no replay"
	// fallback path. Frontends provide this to include their message history.
	SnapshotProvider func() tunnel.BrokerSnapshot

	// OnCommand is called when the mobile client sends a command (message,
	// approval response, etc.). Frontends use this to handle inbound user input.
	OnCommand func(cmd tunnel.GatewayMessage)

	// OnConnected is called when a mobile client connects (role == "client").
	OnConnected func(info tunnel.RelayConnectedState)

	// ClientTag identifies the frontend for relay metadata ("tui", "wails", "daemon").
	ClientTag string
}

// ShareResult holds the connection details returned by StartShare.
type ShareResult struct {
	ConnectURL string
	QRCode     string
	QRCodePNG  []byte
	Token      string
	RoomID     string

	// Broker is the online tunnel broker. Frontends use this to register
	// additional callbacks (OnCommand, OnRelayConnected, etc.) after StartShare.
	Broker *tunnel.Broker

	// Session is the tunnel session object. Frontends store it for cleanup
	// and invite refresh.
	Session *tunnel.Session
}

// tunnelSessionRef holds the active tunnel session + broker for cleanup.
type tunnelSessionRef struct {
	session *tunnel.Session
	broker  *tunnel.Broker
}

// StartShare is the SINGLE ENTRY POINT for starting a mobile tunnel share.
// It performs the complete sequence:
//
//  1. Create + start tunnel session (connect to relay)
//  2. Create broker
//  3. Wire snapshot provider + stream event forwarding
//  4. Cache session info (workspace, model, provider)
//  5. Full canonical share bootstrap (PrepareOnlineShare)
//  6. Return connection details (URL + QR code)
//
// Frontends should call this and then display the QR code.
// Call StopShare to tear down.
func (h *TunnelHost) StartShare(cfg ShareConfig) (*ShareResult, error) {
	if cfg.Workspace == "" {
		return nil, fmt.Errorf("tunnel: workspace is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Create + start tunnel session
	clientTag := cfg.ClientTag
	if clientTag == "" {
		clientTag = "ggcode"
	}
	sess := tunnel.NewSession(tunnel.DefaultRelayURL, tunnel.WithClientMetadata(clientTag, cfg.Version))
	info, err := sess.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("tunnel start: %w", err)
	}

	// 2. Create broker
	broker := tunnel.NewBroker(sess)

	// 3. Wire frontend callbacks onto broker
	if cfg.OnCommand != nil {
		broker.OnCommand(cfg.OnCommand)
	}
	if cfg.OnConnected != nil {
		broker.OnRelayConnected(cfg.OnConnected)
	}
	if cfg.SnapshotProvider != nil {
		broker.SetSnapshotProvider(cfg.SnapshotProvider)
	}

	// 4. Wire stream event forwarding via AttachOnlineBroker
	h.AttachOnlineBroker(broker)

	// 5. Cache session info
	h.SetSessionInfo(tunnel.SessionInfoData{
		Workspace: cfg.Workspace,
		Model:     cfg.Model,
		Provider:  cfg.Provider,
		Mode:      cfg.Mode,
		Version:   cfg.Version,
		Language:  cfg.Language,
	})

	// 6. Run full canonical share bootstrap
	h.PrepareOnlineShare(broker)

	// 7. Store ref for cleanup
	h.mu.Lock()
	h.activeShare = &tunnelSessionRef{session: sess, broker: broker}
	h.mu.Unlock()

	return &ShareResult{
		ConnectURL: info.ConnectURL,
		QRCode:     info.QRCode,
		QRCodePNG:  info.QRCodePNG,
		Token:      info.Token,
		RoomID:     info.RoomID,
		Broker:     broker,
		Session:    sess,
	}, nil
}

// StopShare tears down the active share session and broker.
func (h *TunnelHost) StopShare() {
	h.mu.Lock()
	ref := h.activeShare
	h.activeShare = nil
	h.mu.Unlock()

	h.DetachOnlineBroker()

	if ref != nil {
		if ref.session != nil {
			ref.session.Stop()
		}
	}
}

// GetShareInfo returns the current share connection info (URL + QR), if active.
func (h *TunnelHost) GetShareInfo() *ShareResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeShare == nil || h.activeShare.session == nil {
		return nil
	}
	info := h.activeShare.session.Info()
	if info == nil {
		return nil
	}
	return &ShareResult{
		ConnectURL: info.ConnectURL,
		QRCode:     info.QRCode,
		QRCodePNG:  info.QRCodePNG,
		Token:      info.Token,
		RoomID:     info.RoomID,
	}
}

// attachOnlineBroker connects an online tunnel broker (from a Share session).
// Events recorded by the projection broker will be forwarded to this broker.
func (h *TunnelHost) AttachOnlineBroker(broker *tunnel.Broker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = broker
}

// setSessionInfo caches session metadata (workspace, model, provider, etc.)
// so prepareOnlineShare can include it in session_info and active_session.
func (h *TunnelHost) SetSessionInfo(info tunnel.SessionInfoData) {
	h.mu.Lock()
	// Auto-populate Title from session if caller didn't provide one
	if info.Title == "" && h.session != nil {
		info.Title = h.session.Title
	}
	h.sessionInfo = info
	h.mu.Unlock()
}

// prepareOnlineShare is the CANONICAL share bootstrap for ALL frontends.
// It performs the complete sequence:
//
//  1. SetEventRecorder(nil) — online broker should not record
//  2. SendSessionInfo — cache workspace/model/provider so active_session includes workspace_path
//  3. BindSession + SetAuthorityEpoch — bind online broker to session
//  4. SetReplayProvider — provides canonical replay events from projection store
//  5. Replay or Snapshot — send historical events to mobile
//  6. AnnounceActiveSession — tell relay/mobile which session is active (LAST)
func (h *TunnelHost) PrepareOnlineShare(broker *tunnel.Broker) {
	h.mu.Lock()
	ses := h.session
	projStore := h.projStore
	broken := h.projBroken
	info := h.sessionInfo
	h.mu.Unlock()

	if broker == nil || ses == nil || strings.TrimSpace(ses.ID) == "" {
		return
	}

	// Populate Title from session if not already set (SetSessionInfo may
	// have been called before BindSession, so h.session was nil at that time)
	if info.Title == "" && ses.Title != "" {
		info.Title = ses.Title
		h.mu.Lock()
		h.sessionInfo = info
		h.mu.Unlock()
	}

	// 1. Online broker should NOT record events — projection broker handles that
	broker.SetEventRecorder(nil)

	// 2. Cache session info FIRST so sendActiveSession has workspace_path
	broker.SendSessionInfo(info)

	// 3. Bind online broker to session
	broker.BindSession(ses.ID)

	// 4. Set replay provider from projection store
	broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return h.TunnelEvents()
	})

	// 5. Set authority epoch
	if projStore != nil && !broken {
		if epoch, err := ProjectionAuthorityEpoch(projStore, ses.ID); err == nil && epoch > 0 {
			broker.SetAuthorityEpoch(epoch)
		}
	}

	// 6. Replay projection events or send snapshot if no events
	replay := h.TunnelEvents()
	if len(replay) > 0 {
		broker.ReplayEvents(replay, false)
	} else {
		broker.SendSnapshotFromProvider()
	}

	// 6.5. Re-send session info AFTER replay — replayed events may contain
	// an old session_info (without Title) that overwrites the correct one.
	// Re-sending ensures the last session_info mobile receives has the Title.
	broker.SendSessionInfo(info)

	// 7. Announce active session LAST — after replay so mobile has all history
	// before processing the active_session handshake.
	broker.AnnounceActiveSession(ses.ID)
}

// DetachOnlineBroker disconnects the online broker (Share stopped).
func (h *TunnelHost) DetachOnlineBroker() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = nil
}

// Close stops any active share gracefully and cleans up resources.
func (h *TunnelHost) Close() {
	h.mu.Lock()
	online := h.onlineBroker
	ref := h.activeShare
	h.onlineBroker = nil
	h.projBroker = nil
	h.projStore = nil
	h.activeShare = nil
	h.session = nil
	h.sessionStore = nil
	h.mu.Unlock()

	// Stop the active share's tunnel session first (relay connection).
	if ref != nil && ref.session != nil {
		ref.session.Stop()
	}
	// Gracefully stop any active share so relay and mobile clients are notified
	if online != nil {
		online.StopSharingGracefully(2 * time.Second)
	}
}

// OnlineBroker returns the current online broker, or nil.
func (h *TunnelHost) OnlineBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.onlineBroker
}

// ProjectionBroker returns the projection broker, or nil.
func (h *TunnelHost) ProjectionBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projBroker
}

// ProjectionStore returns the projection store, or nil.
func (h *TunnelHost) ProjectionStore() *tunnel.ProjectionStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projStore
}

// ResetStreamState resets the stream state for a new chat turn.
func (h *TunnelHost) ResetStreamState() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentMsgID = ""
	h.needsFinalize = false
}

// AuthorityEpoch returns the current projection authority epoch.
func (h *TunnelHost) AuthorityEpoch() uint64 {
	h.mu.Lock()
	store := h.projStore
	ses := h.session
	h.mu.Unlock()
	if store == nil || ses == nil {
		return 1
	}
	if epoch, err := ProjectionAuthorityEpoch(store, ses.ID); err == nil {
		return epoch
	}
	return 1
}

// TunnelEvents returns the projection replay events for the current session.
func (h *TunnelHost) TunnelEvents() []tunnel.GatewayMessage {
	h.mu.Lock()
	store := h.projStore
	ses := h.session
	broken := h.projBroken
	h.mu.Unlock()
	if store == nil || ses == nil || broken {
		return nil
	}
	events, err := ProjectionReplay(store, ses.ID)
	if err != nil {
		h.mu.Lock()
		h.projBroken = true
		h.mu.Unlock()
		return nil
	}
	return events
}

// PushStreamEvent pushes a provider stream event through the tunnel.
// This is the main entry point called by the agent stream callback.
func (h *TunnelHost) PushStreamEvent(ev provider.StreamEvent) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}

	switch ev.Type {
	case provider.StreamEventText:
		msgID := h.ensureMsgID(broker)
		h.markActive()
		broker.PushReasoningDone(TunnelReasoningMsgID(msgID))
		broker.PushText(msgID, ev.Text)

	case provider.StreamEventReasoning:
		if chunk := tunnel.NormalizeReasoningChunk(ev.Text); chunk != "" {
			msgID := h.ensureMsgID(broker)
			h.markActive()
			broker.PushReasoning(TunnelReasoningMsgID(msgID), chunk)
		}

	case provider.StreamEventToolCallDone:
		h.rollover(broker, true)
		name := strings.TrimSpace(ev.Tool.Name)
		if name == "" {
			name = "tool"
		}
		// Push raw toolName + args only; mobile decides how to present them
		broker.PushToolCall(ev.Tool.ID, name, "", string(ev.Tool.Arguments), "")

	case provider.StreamEventToolResult:
		h.rollover(broker, false)
		content := ev.Result
		// Format lanchat list results into a human-readable list
		// instead of raw JSON.
		rawArgs := ""
		if ev.Tool.Arguments != nil {
			rawArgs = string(ev.Tool.Arguments)
		}
		if pres, ok := toolpkg.DescribeToolResult(ev.Tool.Name, rawArgs, content, ev.IsError); ok && pres.Payload != "" {
			content = pres.Payload
		}
		if len([]rune(content)) > 2000 {
			content = string([]rune(content)[:1997]) + "\n..."
		}
		broker.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventDone:
		h.rollover(broker, true)

	case provider.StreamEventError:
		h.rollover(broker, true)
		if ev.Error != nil {
			broker.PushError(provider.UserFacingError(ev.Error))
		}
	}
}

// PushUserMessage pushes a user message to the tunnel.
func (h *TunnelHost) PushUserMessage(text string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushUserMessage(text)
}

// PushUserMessageData pushes a user message with custom data to the tunnel.
func (h *TunnelHost) PushUserMessageData(data tunnel.MessageData) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushUserMessageData(data)
}

// PushStatus pushes a status update to the mobile client.
func (h *TunnelHost) PushStatus(status, message string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushStatus(status, message)
}

// PushActivity pushes an activity update to the mobile client.
func (h *TunnelHost) PushActivity(activity string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker == nil {
		return
	}
	broker.PushActivity(activity)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (h *TunnelHost) ensureMsgID(broker *tunnel.Broker) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.currentMsgID == "" {
		h.currentMsgID = broker.NextMessageID()
	}
	return h.currentMsgID
}

func (h *TunnelHost) markActive() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.needsFinalize = true
}

func (h *TunnelHost) rollover(broker *tunnel.Broker, startNew bool) {
	h.mu.Lock()
	msgID := h.currentMsgID
	finalize := h.needsFinalize
	h.mu.Unlock()
	if broker == nil || msgID == "" {
		return
	}
	if finalize {
		broker.PushTextDone(msgID)
	}
	if startNew {
		h.mu.Lock()
		h.currentMsgID = broker.NextMessageID()
		h.needsFinalize = false
		h.mu.Unlock()
	} else {
		h.mu.Lock()
		h.needsFinalize = false
		h.mu.Unlock()
	}
}

// recordEvent is called by the projection broker's event recorder.
// It persists the event to the projection store and forwards to the online broker.
func (h *TunnelHost) recordEvent(ev tunnel.GatewayMessage) {
	// Persist to projection store
	h.mu.Lock()
	projStore := h.projStore
	ses := h.session
	h.mu.Unlock()

	if ev.EventID == "" || ev.Type == tunnel.EventSnapshotReset {
		return
	}

	// Write to projection store (always, even before Share)
	if projStore != nil {
		if err := AppendProjectionEvent(projStore, ev); err != nil {
			h.mu.Lock()
			h.projBroken = true
			h.mu.Unlock()
		}
	}

	// Keep in-memory TunnelEvents for compatibility (desktop CurrentSessionTunnelEvents, etc).
	// Tunnel events are NO LONGER persisted to the session JSONL — the projection
	// store (~/.ggcode/mobile-projection/<sessionID>.json) is the sole durable copy.
	if ses != nil {
		record := session.TunnelEvent{
			EventID:  ev.EventID,
			StreamID: ev.StreamID,
			Type:     ev.Type,
			Data:     append([]byte(nil), ev.Data...),
		}
		ses.TunnelEvents = append(ses.TunnelEvents, record)
		if len(ses.TunnelEvents) > session.MaxTunnelEvents {
			pruneIdx := len(ses.TunnelEvents) - session.MaxTunnelEvents
			ses.TunnelEvents = ses.TunnelEvents[pruneIdx:]
		}
	}

	// Forward to online broker if connected
	h.mu.Lock()
	online := h.onlineBroker
	h.mu.Unlock()
	if online != nil {
		online.PublishRecordedEvent(ev)
	}
}

// TunnelReasoningMsgID is in tunnel_main_stream.go
