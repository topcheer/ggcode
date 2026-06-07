package agentruntime

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// TunnelHost manages the full lifecycle of tunnel event streaming.
// It is owned by InteractiveRuntimeCore and used by all frontends (TUI, daemon, desktop).
//
// Architecture:
//
//	Frontend (TUI/Daemon/Wails)
//	  → agent.RunStream(callback)
//	    → TunnelHost.PushStreamEvent(ev)
//	      → projBroker.Push*()            [always, records events]
//	        → eventRecorder callback
//	          → projStore.Append()         [persist for replay]
//	          → session.TunnelEvents append[persist in session]
//	          → onlineBroker.Publish()     [forward to mobile, if connected]
//
// Frontends only need to:
//  1. Create a TunnelHost (via InteractiveRuntimeCore)
//  2. Call BindSession(session, store) when the session changes
//  3. Call AttachOnlineBroker(broker) when Share starts
//  4. Call DetachOnlineBroker() when Share stops
//  5. Let the core call PushStreamEvent(ev) in the agent stream callback
type TunnelHost struct {
	mu sync.Mutex

	// Projection layer (always active, even before Share)
	projBroker *tunnel.Broker
	projStore  *tunnel.ProjectionStore
	projBroken bool

	// Online layer (active when Share is on)
	onlineBroker *tunnel.Broker

	// Message stream state
	currentMsgID  string
	needsFinalize bool

	// Session reference for recording
	session      *session.Session
	sessionStore session.Store

	// Optional callback to describe a tool for mobile display.
	// Returns (displayName, detail). If nil, both will be empty strings.
}

// NewTunnelHost creates a new TunnelHost with an offline projection broker.
func NewTunnelHost() *TunnelHost {
	h := &TunnelHost{
		projBroker: tunnel.NewBroker(nil), // offline broker, no relay
	}
	// Always set event recorder so projBroker.Push*() calls are forwarded
	h.projBroker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
		h.recordEvent(ev)
	})
	return h
}

// BindSession binds the tunnel host to a session for event recording.
// Call this when the session changes or is first created.
func (h *TunnelHost) BindSession(ses *session.Session, store session.Store) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.session = ses
	h.sessionStore = store
	h.currentMsgID = ""
	h.needsFinalize = false

	if ses == nil || strings.TrimSpace(ses.ID) == "" {
		return
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
	_, _ = PrepareProjectionBroker(h.projBroker, h.projStore, ses, func(ev tunnel.GatewayMessage) {
		h.recordEvent(ev)
	})
}

// AttachOnlineBroker connects an online tunnel broker (from a Share session).
// Events recorded by the projection broker will be forwarded to this broker.
func (h *TunnelHost) AttachOnlineBroker(broker *tunnel.Broker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = broker
}

// DetachOnlineBroker disconnects the online broker (Share stopped).
func (h *TunnelHost) DetachOnlineBroker() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = nil
}

// OnlineBroker returns the current online broker, or nil.
func (h *TunnelHost) OnlineBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.onlineBroker
}

// ProjectionBroker returns the projection broker for replay/attach operations.
func (h *TunnelHost) ProjectionBroker() *tunnel.Broker {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projBroker
}

// ProjectionStore returns the projection store for replay queries.
func (h *TunnelHost) ProjectionStore() *tunnel.ProjectionStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projStore
}

// IsProjectionBroken returns whether the projection store is in a broken state.
func (h *TunnelHost) IsProjectionBroken() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.projBroken
}

// PushStreamEvent pushes a provider stream event to the mobile client.
// This is the single entry point that all frontends should use in their
// agent stream callback.
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
		if msgID == "" {
			return
		}
		h.markActive()
		broker.PushReasoningDone(TunnelReasoningMsgID(msgID))
		broker.PushText(msgID, ev.Text)

	case provider.StreamEventReasoning:
		if chunk := tunnel.NormalizeReasoningChunk(ev.Text); chunk != "" {
			msgID := h.ensureMsgID(broker)
			if msgID == "" {
				return
			}
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
			runes := []rune(content)
			content = string(runes[:minInt(len(runes), 1997)]) + "\n...(truncated)"
		}
		broker.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)

	case provider.StreamEventSystem:
		h.rollover(broker, true)

	case provider.StreamEventDone:
		h.rollover(broker, true)

	case provider.StreamEventError:
		h.rollover(broker, true)
		if ev.Error != nil {
			broker.PushError(provider.UserFacingError(ev.Error))
		}
	}
}

// PushUserMessage pushes a user message to the mobile client.
func (h *TunnelHost) PushUserMessage(text string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker != nil {
		broker.PushUserMessage(text)
	}
}

// PushUserMessageData pushes a user message with metadata to the mobile client.
func (h *TunnelHost) PushUserMessageData(data tunnel.MessageData) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker != nil {
		broker.PushUserMessageData(data)
	}
}

// PushStatus pushes a status update to the mobile client.
func (h *TunnelHost) PushStatus(status, message string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker != nil {
		broker.PushStatus(status, message)
	}
}

// PushActivity pushes an activity update to the mobile client.
func (h *TunnelHost) PushActivity(activity string) {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker != nil {
		broker.PushActivity(strings.TrimSpace(activity))
	}
}

// NextMessageID returns the next message ID from the projection broker.
func (h *TunnelHost) NextMessageID() string {
	h.mu.Lock()
	broker := h.projBroker
	h.mu.Unlock()
	if broker != nil {
		return broker.NextMessageID()
	}
	return ""
}

// CurrentMsgID returns the current stream message ID.
func (h *TunnelHost) CurrentMsgID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.currentMsgID
}

// ResetStreamState resets the message stream state for a new round.
func (h *TunnelHost) ResetStreamState() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.needsFinalize = false
}

// TunnelEvents returns recorded tunnel events for the current session (for replay).
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

// AuthorityEpoch returns the projection authority epoch for the current session.
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

// Close cleans up tunnel host resources.
func (h *TunnelHost) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onlineBroker = nil
	h.projBroker = nil
}

// ── Internal helpers ──

func (h *TunnelHost) ensureMsgID(broker *tunnel.Broker) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.currentMsgID == "" && broker != nil {
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
		broker.PushReasoningDone(TunnelReasoningMsgID(msgID))
		broker.PushTextDone(msgID)
	}
	h.mu.Lock()
	if startNew && broker != nil {
		h.currentMsgID = broker.NextMessageID()
	}
	h.needsFinalize = false
	h.mu.Unlock()
}

// recordEvent is the event recorder callback for the projection broker.
// It writes to the projection store, records in session, and forwards to online broker.
func (h *TunnelHost) recordEvent(ev tunnel.GatewayMessage) {
	h.mu.Lock()
	store := h.projStore
	broken := h.projBroken
	online := h.onlineBroker
	ses := h.session
	sesStore := h.sessionStore
	h.mu.Unlock()

	// 1. Write to projection store
	if store != nil && !broken {
		if err := AppendProjectionEvent(store, ev); err != nil {
			h.mu.Lock()
			h.projBroken = true
			h.mu.Unlock()
		}
	}

	// 2. Record in session
	if ses != nil && sesStore != nil && ev.EventID != "" && ev.Type != tunnel.EventSnapshotReset {
		record := session.TunnelEvent{
			EventID:  ev.EventID,
			StreamID: ev.StreamID,
			Type:     ev.Type,
			Data:     append([]byte(nil), ev.Data...),
		}
		ses.TunnelEvents = append(ses.TunnelEvents, record)
	}

	// 3. Forward to online broker
	if online != nil {
		online.PublishRecordedEvent(ev)
	}
}

// forwardToOnline pushes an event directly to the online broker.
// Used as fallback when the projection broker has no event recorder
// (e.g. before BindSession was called).
func (h *TunnelHost) forwardToOnline(ev tunnel.GatewayMessage) {
	h.mu.Lock()
	online := h.onlineBroker
	h.mu.Unlock()
	if online != nil {
		online.PublishRecordedEvent(ev)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
