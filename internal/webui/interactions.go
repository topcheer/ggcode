package webui

import (
	"sync"

	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

// #5865: the webui is a first-class remote surface (it drives the same agent
// as the TUI through the chat bridge), but an ask-type gate was never
// surfaced to it - the TUI notified IM and the mobile tunnel only, so a
// browser session went silent until the approval timed out. The
// InteractionHub is the webui-side counterpart of the tunnel approval
// channel: hosts publish pending approvals / ask_user requests, browser tabs
// subscribe, and a response is routed back to the host's responder.
const (
	InteractionKindApproval = "approval"
	InteractionKindAskUser  = "ask_user"

	InteractionTypeRequest  = "interaction"
	InteractionTypeResolved = "interaction_resolved"

	InteractionDecisionAllow       = "allow"
	InteractionDecisionAlwaysAllow = "always_allow"
	InteractionDecisionDeny        = "deny"
	InteractionDecisionTimeout     = "timeout"
	InteractionDecisionCancelled   = "cancelled"
)

// InteractionEvent is what a browser tab receives over /api/chat/ws.
type InteractionEvent struct {
	Type     string               `json:"type"`
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	ToolName string               `json:"tool_name,omitempty"`
	Input    string               `json:"input,omitempty"`
	Request  *tool.AskUserRequest `json:"request,omitempty"`
	Decision string               `json:"decision,omitempty"`
}

// InteractionResponse is what a browser tab sends back.
type InteractionResponse struct {
	Type string `json:"type"` // "interaction_response"
	ID   string `json:"id"`
	// Approval: allow | always_allow | deny. AskUser: answered | cancelled.
	Decision string                 `json:"decision"`
	Answers  []tunnel.AskUserAnswer `json:"answers,omitempty"`
}

// InteractionResponder receives a browser response for a still-pending
// request. It runs on the websocket read goroutine - hosts must not block.
type InteractionResponder func(InteractionResponse)

// InteractionBridge is the optional surface a ChatBridge implements when it
// can carry ask-type interactions. The websocket handler type-asserts it.
type InteractionBridge interface {
	SubscribeInteractions(fn func(InteractionEvent)) func()
	PendingInteractions() []InteractionEvent
	RespondInteraction(resp InteractionResponse) bool
}

// InteractionHub fans pending interactions out to tabs and routes responses.
// It is safe for concurrent use; zero value is not usable, use
// NewInteractionHub.
type InteractionHub struct {
	mu        sync.Mutex
	subs      map[int]func(InteractionEvent)
	nextSub   int
	pending   map[string]InteractionEvent
	order     []string
	responder InteractionResponder
}

func NewInteractionHub() *InteractionHub {
	return &InteractionHub{
		subs:    make(map[int]func(InteractionEvent)),
		pending: make(map[string]InteractionEvent),
	}
}

// SetResponder installs the host's response router (replaces any previous).
func (h *InteractionHub) SetResponder(fn InteractionResponder) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.responder = fn
	h.mu.Unlock()
}

// PublishRequest makes a new pending interaction visible to every tab.
func (h *InteractionHub) PublishRequest(ev InteractionEvent) {
	if h == nil || ev.ID == "" {
		return
	}
	ev.Type = InteractionTypeRequest
	h.mu.Lock()
	if _, dup := h.pending[ev.ID]; !dup {
		h.order = append(h.order, ev.ID)
	}
	h.pending[ev.ID] = ev
	subs := make([]func(InteractionEvent), 0, len(h.subs))
	for _, fn := range h.subs {
		subs = append(subs, fn)
	}
	h.mu.Unlock()
	for _, fn := range subs {
		fn(ev)
	}
}

// PublishResult resolves a pending interaction (or announces a resolution
// that happened elsewhere, e.g. on the TUI) so every tab clears it.
func (h *InteractionHub) PublishResult(kind, id, decision string) {
	if h == nil || id == "" {
		return
	}
	h.mu.Lock()
	delete(h.pending, id)
	for i, oid := range h.order {
		if oid == id {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	subs := make([]func(InteractionEvent), 0, len(h.subs))
	for _, fn := range h.subs {
		subs = append(subs, fn)
	}
	h.mu.Unlock()
	ev := InteractionEvent{Type: InteractionTypeResolved, Kind: kind, ID: id, Decision: decision}
	for _, fn := range subs {
		fn(ev)
	}
}

// Pending returns the open interactions in publication order (snapshot for
// a tab that connects late).
func (h *InteractionHub) Pending() []InteractionEvent {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]InteractionEvent, 0, len(h.order))
	for _, id := range h.order {
		if ev, ok := h.pending[id]; ok {
			out = append(out, ev)
		}
	}
	return out
}

// HasSubscribers reports whether at least one browser tab is listening.
// Hosts use it to decide whether waiting for a browser answer can pay off.
func (h *InteractionHub) HasSubscribers() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}

// Subscribe registers a tab sink; the returned func unsubscribes.
func (h *InteractionHub) Subscribe(fn func(InteractionEvent)) func() {
	if h == nil || fn == nil {
		return func() {}
	}
	h.mu.Lock()
	h.nextSub++
	id := h.nextSub
	h.subs[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// Respond routes a browser response to the host. It reports whether the
// response matched a still-pending request; stale/duplicate responses are
// dropped (the tab already saw interaction_resolved).
func (h *InteractionHub) Respond(resp InteractionResponse) bool {
	if h == nil || resp.ID == "" {
		return false
	}
	h.mu.Lock()
	_, pending := h.pending[resp.ID]
	responder := h.responder
	h.mu.Unlock()
	if !pending || responder == nil {
		return false
	}
	responder(resp)
	return true
}

// CancelAll resolves everything as cancelled (session teardown).
func (h *InteractionHub) CancelAll() {
	if h == nil {
		return
	}
	for _, ev := range h.Pending() {
		h.PublishResult(ev.Kind, ev.ID, InteractionDecisionCancelled)
	}
}
