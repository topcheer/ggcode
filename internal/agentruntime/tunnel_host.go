package agentruntime

import (
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
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

// AttachOnlineBroker connects an online tunnel broker (from a Share session).
// Events recorded by the projection broker will be forwarded to this broker.
func (h *TunnelHost) AttachOnlineBroker(broker *tunnel.Broker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = broker
}

// PrepareOnlineShare configures an online broker for a fresh Share session.
// This must be called after AttachOnlineBroker. It performs all the
// negotiation steps that TUI's handleTunnelStartMsg does:
//
//   - SetEventRecorder(nil) — online broker should not record; projection broker does that
//   - SetReplayProvider — provides canonical replay events from projection store
//   - BindSession + SetAuthorityEpoch — binds online broker to current session
//   - AnnounceActiveSession — tells relay which session is active
//
// Returns the replay events so the caller can also ReplayEvents() if needed.
func (h *TunnelHost) PrepareOnlineShare(broker *tunnel.Broker) []tunnel.GatewayMessage {
	h.mu.Lock()
	ses := h.session
	projStore := h.projStore
	broken := h.projBroken
	h.mu.Unlock()

	if broker == nil || ses == nil || strings.TrimSpace(ses.ID) == "" {
		return nil
	}

	// Online broker should NOT record events — projection broker handles that
	broker.SetEventRecorder(nil)

	// Bind online broker to session
	broker.BindSession(ses.ID)

	// Set replay provider from projection store
	broker.SetReplayProvider(func() []tunnel.GatewayMessage {
		return h.TunnelEvents()
	})

	// Set authority epoch
	if projStore != nil && !broken {
		if epoch, err := ProjectionAuthorityEpoch(projStore, ses.ID); err == nil && epoch > 0 {
			broker.SetAuthorityEpoch(epoch)
		}
	}

	// Replay projection events to online broker first, then announce.
	// Mobile expects: replay → session_info/status/activity → active_session.
	replay := h.TunnelEvents()
	if len(replay) > 0 {
		broker.ReplayEvents(replay, false)
	}

	// Announce active session LAST — after replay so mobile has all history
	// before processing the active_session handshake.
	broker.AnnounceActiveSession(ses.ID)

	return replay
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
	h.onlineBroker = nil
	h.projBroker = nil
	h.projStore = nil
	h.session = nil
	h.sessionStore = nil
	h.mu.Unlock()

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
		h.rollover(broker, false)
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
// It persists the event to the session and forwards to the online broker.
func (h *TunnelHost) recordEvent(ev tunnel.GatewayMessage) {
	// Persist to session
	h.mu.Lock()
	ses := h.session
	store := h.sessionStore
	h.mu.Unlock()

	if ses == nil || ev.EventID == "" || ev.Type == tunnel.EventSnapshotReset {
		return
	}
	if store == nil {
		return
	}

	record := session.TunnelEvent{
		EventID:  ev.EventID,
		StreamID: ev.StreamID,
		Type:     ev.Type,
		Data:     append([]byte(nil), ev.Data...),
	}
	ses.TunnelEvents = append(ses.TunnelEvents, record)

	if jsonlStore, ok := store.(*session.JSONLStore); ok {
		_ = jsonlStore.AppendTunnelEventToDisk(ses, record)
	} else {
		_ = store.Save(ses)
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
