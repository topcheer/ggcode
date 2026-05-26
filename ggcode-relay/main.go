package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	peerWriteTimeout  = 30 * time.Second
	peerReadTimeout   = 75 * time.Second
	relayOfflineGrace = 90 * time.Second
	retryAfterOffline = 60 * time.Second
)

// relayMessage is the wire format. Metadata fields remain plaintext so relay
// can manage replay and ordering without decrypting business payloads.
type relayMessage struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"session_id,omitempty"`
	EventID     string          `json:"event_id,omitempty"`
	StreamID    string          `json:"stream_id,omitempty"`
	ClientID    string          `json:"client_id,omitempty"`
	MessageID   string          `json:"message_id,omitempty"`
	LastEventID string          `json:"last_event_id,omitempty"`
	ResumeMode  string          `json:"resume_mode,omitempty"`
	Nonce       string          `json:"nonce,omitempty"`
	Ciphertext  string          `json:"ciphertext,omitempty"`
	Role        string          `json:"role,omitempty"`
	Count       int             `json:"count,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

type roomEvent struct {
	sessionID string
	eventID   string
	streamID  string
	typ       string
	raw       []byte
}

// ─── Room ───

type room struct {
	token        string
	server       *peer
	clients      map[*peer]struct{}
	clientsByID  map[string]*peer
	sessionID    string
	history      []roomEvent
	mu           sync.RWMutex
	offlineTimer *time.Timer
}

type relayOfflineData struct {
	Reason       string `json:"reason,omitempty"`
	RetryAfterMS int    `json:"retry_after_ms,omitempty"`
}

func (r *room) upsertHistoryEvent(ev roomEvent) {
	if ev.sessionID == "" || ev.eventID == "" {
		return
	}
	for i := range r.history {
		if r.history[i].sessionID == ev.sessionID && r.history[i].eventID == ev.eventID {
			r.history[i] = ev
			return
		}
	}
	r.history = append(r.history, ev) // grace period before notifying clients
}

func newRoom(token string) *room {
	return &room{
		token:       token,
		clients:     make(map[*peer]struct{}),
		clientsByID: make(map[string]*peer),
	}
}

func serverOfflineNotice(sessionID string) relayMessage {
	return relayMessage{
		Type:      "server_offline",
		SessionID: sessionID,
		Data: mustJSON(relayOfflineData{
			Reason:       "server_offline",
			RetryAfterMS: int(retryAfterOffline / time.Millisecond),
		}),
	}
}

func (r *room) notifyServerClientConnected() {
	if r.server == nil {
		return
	}
	lastEventID := ""
	if n := len(r.history); n > 0 {
		lastEventID = r.history[n-1].eventID
	}
	r.server.sendJSON(relayMessage{
		Type:        "connected",
		Role:        "client",
		SessionID:   r.sessionID,
		Count:       len(r.history),
		LastEventID: lastEventID,
	})
}

// ─── Peer ───
//
// writePump is the ONLY goroutine that writes to conn.
// It sends: connected → replay (clients only) → live messages from sendCh.
// This guarantees strict FIFO ordering — replay and live broadcasts never interleave.

type peer struct {
	hub      *hub
	room     *room
	role     string // "server" or "client"
	conn     *websocket.Conn
	sendCh   chan []byte // never closed; done channel signals stop
	done     chan struct{}
	clientID string
	ready    bool
}

// sendJSON enqueues a message for sending. Never panics — sendCh is never closed.
// It applies backpressure instead of dropping messages; replay gaps are more
// harmful than temporarily slowing a room.
func (p *peer) sendJSON(msg relayMessage) {
	data, _ := json.Marshal(msg)
	select {
	case p.sendCh <- data:
	case <-p.done:
	}
}

func (p *peer) sendRaw(raw []byte) {
	select {
	case p.sendCh <- raw:
	case <-p.done:
	}
}

func (p *peer) writePump() {
	defer p.conn.Close()

	// 1. Send connected confirmation directly.
	p.room.mu.RLock()
	lastEventID := ""
	if n := len(p.room.history); n > 0 {
		lastEventID = p.room.history[n-1].eventID
	}
	connState := relayMessage{
		Type:        "connected",
		Role:        p.role,
		SessionID:   p.room.sessionID,
		Count:       len(p.room.history),
		LastEventID: lastEventID,
	}
	p.room.mu.RUnlock()
	connMsg, _ := json.Marshal(connState)
	if p.hub != nil {
		p.hub.traceRelayMessage("peer_init", p.room.token, p.clientID, connState, "")
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(peerWriteTimeout))
	if err := p.conn.WriteMessage(websocket.TextMessage, connMsg); err != nil {
		return
	}
	if connState.SessionID != "" {
		activeRelayMsg := relayMessage{
			Type:      "active_session",
			SessionID: connState.SessionID,
			Data:      mustJSON(activeSessionData{SessionID: connState.SessionID}),
		}
		activeMsg, _ := json.Marshal(activeRelayMsg)
		if p.hub != nil {
			p.hub.traceRelayMessage("peer_init", p.room.token, p.clientID, activeRelayMsg, "")
		}
		_ = p.conn.SetWriteDeadline(time.Now().Add(peerWriteTimeout))
		if err := p.conn.WriteMessage(websocket.TextMessage, activeMsg); err != nil {
			return
		}
	}

	// 2. Normal pump — resume ack/replay/live messages all flow through sendCh.
	for {
		select {
		case msg := <-p.sendCh:
			_ = p.conn.SetWriteDeadline(time.Now().Add(peerWriteTimeout))
			if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-p.done:
			return
		}
	}
}

func (p *peer) readPump(h *hub) {
	roomDestroyed := false
	defer func() {
		close(p.done) // signal writePump to stop
		p.conn.Close()
		p.room.mu.Lock()
		if p.role == "server" {
			p.room.server = nil
			token := p.room.token
			sessionID := p.room.sessionID
			p.room.mu.Unlock()
			if !roomDestroyed {
				h.notifyRoomRecovering(token, sessionID)
				h.scheduleRoomExpiry(token)
			}
		} else {
			delete(p.room.clients, p)
			if p.clientID != "" {
				if current := p.room.clientsByID[p.clientID]; current == p {
					delete(p.room.clientsByID, p.clientID)
				}
			}
			p.room.mu.Unlock()
			h.removeRoomIfEmpty(p.room)
		}
		if p.hub != nil && p.hub.stats != nil {
			p.hub.stats.recordDisconnect(p.role)
		}
		log.Printf("[relay] %s disconnected: room=%s", p.role, shortToken(p.room.token))
	}()

	p.conn.SetReadLimit(1 << 20)
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		return nil
	})

	for {
		p.conn.SetReadDeadline(time.Now().Add(peerReadTimeout))
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg relayMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "ping":
			p.sendJSON(relayMessage{Type: "pong"})
			continue
		case "destroy_room":
			if p.role == "server" {
				if p.hub != nil {
					p.hub.traceRelayMessage("server_request", p.room.token, p.clientID, msg, "")
				}
				roomDestroyed = true
				h.destroyRoom(p.room.token, relayMessage{Type: "sharing_stopped"})
				return
			}
			continue
		case "active_session":
			if p.role == "server" {
				if p.hub != nil {
					p.hub.traceRelayMessage("server_request", p.room.token, p.clientID, msg, "")
				}
				p.handleActiveSession(msg)
			}
			continue
		case "resume_hello", "resume_from":
			if p.role == "client" {
				if p.hub != nil {
					p.hub.traceRelayMessage("client_request", p.room.token, msg.ClientID, msg, "")
				}
				p.handleResume(msg)
			}
			continue
		case "language_change":
			if p.hub != nil {
				p.hub.traceRelayMessage("client_request", p.room.token, p.clientID, msg, "")
			}
			// forward to all other peers in room
			p.room.mu.Lock()
			fwdMsg := relayMessage{
				Type:      "language_change",
				Data:      msg.Data,
				SessionID: p.room.sessionID,
			}
			if p.room.server != nil && p.room.server != p {
				p.room.server.sendJSON(fwdMsg)
			}
			for c := range p.room.clients {
				if c != p {
					c.sendJSON(fwdMsg)
				}
			}
			p.room.mu.Unlock()
			continue
		case "theme_change":
			if p.hub != nil {
				p.hub.traceRelayMessage("client_request", p.room.token, p.clientID, msg, "")
			}
			p.room.mu.Lock()
			fwdMsg := relayMessage{
				Type:      "theme_change",
				Data:      msg.Data,
				SessionID: p.room.sessionID,
			}
			if p.room.server != nil && p.room.server != p {
				p.room.server.sendJSON(fwdMsg)
			}
			for c := range p.room.clients {
				if c != p {
					c.sendJSON(fwdMsg)
				}
			}
			p.room.mu.Unlock()
			continue
		case "encrypted":
			if p.hub != nil {
				route := "server_publish"
				if p.role == "client" {
					route = "client_forward"
				}
				p.hub.traceRelayMessage(route, p.room.token, p.clientID, msg, "")
			}
			// Send relay_ack back to the client immediately if message_id is present.
			if p.role == "client" && msg.MessageID != "" {
				p.sendJSON(relayMessage{Type: "relay_ack", MessageID: msg.MessageID})
			}
			// proceed
		default:
			continue
		}

		p.room.mu.Lock()
		var persistMsg relayMessage
		var persistRaw []byte
		if p.role == "server" {
			readyClients := 0
			if msg.SessionID != "" && msg.SessionID != p.room.sessionID {
				p.room.sessionID = msg.SessionID
				p.room.history = nil
			}
			if msg.SessionID != "" {
				p.room.upsertHistoryEvent(roomEvent{
					sessionID: msg.SessionID,
					eventID:   msg.EventID,
					streamID:  msg.StreamID,
					typ:       msg.Type,
					raw:       append([]byte(nil), raw...),
				})
				persistMsg = msg
				persistRaw = append([]byte(nil), raw...)
			}
			for c := range p.room.clients {
				if c.ready {
					readyClients++
					c.sendRaw(raw)
				}
			}
			if p.hub != nil && readyClients > 0 {
				p.hub.traceRelayMessage("server_broadcast", p.room.token, "", msg, fmt.Sprintf("deliveries=%d", readyClients))
			}
			if p.hub != nil && p.hub.stats != nil {
				p.hub.stats.recordClientBroadcast(readyClients)
			}
		} else {
			if p.room.server != nil {
				p.room.server.sendRaw(raw)
				if p.hub != nil && p.hub.stats != nil {
					p.hub.stats.recordForwardToServer()
				}
			}
		}
		p.room.mu.Unlock()
		if p.role == "server" && p.hub != nil && p.hub.store != nil && persistMsg.SessionID != "" {
			if err := p.hub.store.persistEvent(p.room.token, persistMsg, persistRaw); err != nil {
				if p.hub.stats != nil {
					p.hub.stats.recordPersistResult(false)
				}
				log.Printf("[relay] persist event failed: room=%s err=%v", shortToken(p.room.token), err)
			} else if p.hub.stats != nil {
				p.hub.stats.recordPersistResult(true)
			}
		}
	}
}

type activeSessionData struct {
	SessionID string `json:"session_id"`
}

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func activeSessionID(msg relayMessage) string {
	if msg.SessionID != "" {
		return msg.SessionID
	}
	if len(msg.Data) == 0 {
		return ""
	}
	var data activeSessionData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		return ""
	}
	return data.SessionID
}

func (p *peer) handleActiveSession(msg relayMessage) {
	sessionID := activeSessionID(msg)
	if sessionID == "" {
		return
	}
	var clients []*peer
	var hydrate bool
	hydratedEvents := 0
	p.room.mu.Lock()
	changed := p.room.sessionID != sessionID
	p.room.sessionID = sessionID
	if changed {
		p.room.history = nil
	}
	hydrate = len(p.room.history) == 0
	for c := range p.room.clients {
		clients = append(clients, c)
	}
	token := p.room.token
	p.room.mu.Unlock()

	if hydrate && p.hub != nil && p.hub.store != nil {
		history, err := p.hub.store.loadSessionHistory(sessionID)
		if err != nil {
			log.Printf("[relay] load session history failed: session=%s err=%v", sessionID, err)
		} else if len(history) > 0 {
			hydratedEvents = len(history)
			p.room.mu.Lock()
			if p.room.sessionID == sessionID && len(p.room.history) == 0 {
				p.room.history = history
			}
			p.room.mu.Unlock()
		}
	}

	active := relayMessage{
		Type:      "active_session",
		SessionID: sessionID,
		Data:      mustJSON(activeSessionData{SessionID: sessionID}),
	}
	for _, c := range clients {
		if p.hub != nil {
			p.hub.traceRelayMessage("relay_push", token, c.clientID, active, "reason=active_session")
		}
		c.sendJSON(active)
	}
	if p.hub != nil && p.hub.stats != nil {
		p.hub.stats.recordActiveSession(changed, hydratedEvents)
	}
	log.Printf(
		"[relay] active_session room=%s session=%s changed=%t hydrated=%d clients=%d",
		shortToken(token),
		sessionID,
		changed,
		hydratedEvents,
		len(clients),
	)
	if p.hub != nil && p.hub.store != nil {
		if err := p.hub.store.persistActiveSession(token, sessionID); err != nil {
			if p.hub.stats != nil {
				p.hub.stats.recordPersistResult(false)
			}
			log.Printf("[relay] persist active session failed: room=%s err=%v", shortToken(token), err)
		}
	}
}

func (p *peer) handleResume(msg relayMessage) {
	p.room.mu.Lock()
	defer p.room.mu.Unlock()

	if msg.ClientID == "" {
		p.sendJSON(relayMessage{Type: "error", Ciphertext: "missing client_id"})
		return
	}

	p.clientID = msg.ClientID
	p.room.clientsByID[msg.ClientID] = p

	mode, replay := p.room.resumePlan(msg.SessionID, msg.LastEventID)
	p.ready = true

	p.sendJSON(relayMessage{
		Type:       "resume_ack",
		SessionID:  p.room.sessionID,
		ClientID:   msg.ClientID,
		ResumeMode: mode,
	})
	if p.hub != nil {
		p.hub.traceRelayMessage("relay_push", p.room.token, msg.ClientID, relayMessage{
			Type:       "resume_ack",
			SessionID:  p.room.sessionID,
			ClientID:   msg.ClientID,
			ResumeMode: mode,
		}, fmt.Sprintf("replay=%d", len(replay)))
	}

	switch mode {
	case "snapshot_required":
		resumeMiss := relayMessage{Type: "resume_miss", SessionID: p.room.sessionID, ClientID: msg.ClientID}
		snapshotReset := relayMessage{Type: "snapshot_reset", SessionID: p.room.sessionID, ClientID: msg.ClientID}
		p.sendJSON(resumeMiss)
		p.sendJSON(snapshotReset)
		if p.hub != nil {
			p.hub.traceRelayMessage("relay_push", p.room.token, msg.ClientID, resumeMiss, "")
			p.hub.traceRelayMessage("relay_push", p.room.token, msg.ClientID, snapshotReset, "")
		}
		for _, ev := range replay {
			if p.hub != nil {
				p.hub.traceRoomEvent("replay_send", p.room.token, msg.ClientID, ev, "mode=snapshot_required")
			}
			p.sendRaw(ev.raw)
		}
	default:
		for _, ev := range replay {
			if p.hub != nil {
				p.hub.traceRoomEvent("replay_send", p.room.token, msg.ClientID, ev, "mode="+mode)
			}
			p.sendRaw(ev.raw)
		}
	}
	if p.hub != nil && p.hub.stats != nil {
		p.hub.stats.recordResume(mode, len(replay))
	}
	log.Printf(
		"[relay] resume room=%s client=%s session=%s mode=%s replay=%d",
		shortToken(p.room.token),
		msg.ClientID,
		p.room.sessionID,
		mode,
		len(replay),
	)
}

func (r *room) resumePlan(clientSessionID, lastEventID string) (string, []roomEvent) {
	if len(r.history) == 0 {
		return "full_history", nil
	}
	if clientSessionID == "" || clientSessionID != r.sessionID || lastEventID == "" {
		replay := make([]roomEvent, len(r.history))
		copy(replay, r.history)
		return "full_history", replay
	}
	for i, ev := range r.history {
		if ev.eventID == lastEventID {
			replay := make([]roomEvent, len(r.history[i+1:]))
			copy(replay, r.history[i+1:])
			return "incremental", replay
		}
	}
	replay := make([]roomEvent, len(r.history))
	copy(replay, r.history)
	return "snapshot_required", replay
}

// ─── Hub ───

type hub struct {
	rooms  map[string]*room
	store  *relayStore
	stats  *relayStats
	tracer *relayTraceLogger
	mu     sync.RWMutex
}

func newHub(store *relayStore) *hub {
	return &hub{
		rooms:  make(map[string]*room),
		store:  store,
		stats:  newRelayStats(),
		tracer: newRelayTraceLogger(),
	}
}

func (h *hub) getOrCreateServerRoom(token string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[token]; ok {
		return r
	}
	r := newRoom(token)
	if h.store != nil {
		state, err := h.store.loadRoom(token)
		if err != nil {
			log.Printf("[relay] load room failed: room=%s err=%v", shortToken(token), err)
		} else {
			r.sessionID = state.sessionID
			r.history = state.history
			if h.stats != nil {
				h.stats.recordRoomStoreResult(state.sessionID != "" || len(state.history) > 0)
			}
		}
	}
	h.rooms[token] = r
	return r
}

func (h *hub) getOrLoadClientRoom(token string) (*room, bool) {
	h.mu.RLock()
	if r, ok := h.rooms[token]; ok {
		h.mu.RUnlock()
		return r, true
	}
	h.mu.RUnlock()

	if h.store == nil {
		return nil, false
	}
	state, err := h.store.loadRoom(token)
	if err != nil {
		log.Printf("[relay] load room failed: room=%s err=%v", shortToken(token), err)
		return nil, false
	}
	if state.sessionID == "" && len(state.history) == 0 {
		if h.stats != nil {
			h.stats.recordRoomStoreResult(false)
		}
		return nil, false
	}
	if h.stats != nil {
		h.stats.recordRoomStoreResult(true)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[token]; ok {
		return r, true
	}
	r := newRoom(token)
	r.sessionID = state.sessionID
	r.history = state.history
	h.rooms[token] = r
	return r, true
}

func (h *hub) removeRoomIfEmpty(r *room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r.mu.RLock()
	empty := r.server == nil && len(r.clients) == 0 && r.offlineTimer == nil
	r.mu.RUnlock()
	if empty {
		delete(h.rooms, r.token)
	}
}

func (h *hub) notifyRoomRecovering(token, sessionID string) {
	h.mu.RLock()
	r := h.rooms[token]
	h.mu.RUnlock()
	if r == nil {
		return
	}
	notice := serverOfflineNotice(sessionID)
	r.mu.RLock()
	clients := make([]*peer, 0, len(r.clients))
	for c := range r.clients {
		clients = append(clients, c)
	}
	r.mu.RUnlock()
	if len(clients) == 0 {
		return
	}
	for _, c := range clients {
		if h != nil {
			h.traceRelayMessage("relay_notice", token, c.clientID, notice, "reason=server_recovering")
		}
		c.sendJSON(notice)
	}
	time.AfterFunc(500*time.Millisecond, func() {
		for _, c := range clients {
			if c.conn != nil {
				_ = c.conn.Close()
			}
		}
	})
}

func (h *hub) scheduleRoomExpiry(token string) {
	h.mu.RLock()
	r := h.rooms[token]
	h.mu.RUnlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.offlineTimer != nil {
		r.offlineTimer.Stop()
	}
	r.offlineTimer = time.AfterFunc(relayOfflineGrace, func() {
		h.expireRoomIfServerOffline(token)
	})
	r.mu.Unlock()
}

func (h *hub) expireRoomIfServerOffline(token string) {
	h.mu.RLock()
	r := h.rooms[token]
	h.mu.RUnlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.server != nil {
		r.offlineTimer = nil
		r.mu.Unlock()
		return
	}
	r.offlineTimer = nil
	r.mu.Unlock()
	log.Printf("[relay] server grace period expired: room=%s", shortToken(token))
	h.destroyRoom(token, relayMessage{Type: "server_offline"})
}

func (h *hub) destroyRoom(token string, notice relayMessage) {
	h.mu.Lock()
	r := h.rooms[token]
	if r != nil {
		delete(h.rooms, token)
	}
	h.mu.Unlock()

	var clients []*peer
	if r != nil {
		r.mu.Lock()
		if r.offlineTimer != nil {
			r.offlineTimer.Stop()
			r.offlineTimer = nil
		}
		for c := range r.clients {
			clients = append(clients, c)
		}
		r.server = nil
		r.clients = make(map[*peer]struct{})
		r.clientsByID = make(map[string]*peer)
		r.sessionID = ""
		r.history = nil
		r.mu.Unlock()
	}
	if h.store != nil {
		if err := h.store.destroyRoom(token); err != nil {
			log.Printf("[relay] destroy room failed: room=%s err=%v", shortToken(token), err)
		}
	}
	if h.stats != nil {
		h.stats.recordRoomDestroy()
	}
	log.Printf(
		"[relay] destroy room=%s reason=%s clients=%d",
		shortToken(token),
		notice.Type,
		len(clients),
	)
	if notice.Type != "" {
		for _, c := range clients {
			h.traceRelayMessage("relay_notice", token, c.clientID, notice, "reason=room_destroy")
			c.sendJSON(notice)
		}
		time.AfterFunc(500*time.Millisecond, func() {
			for _, c := range clients {
				_ = c.conn.Close()
			}
		})
	}
}

// ─── HTTP handler ───

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	role := r.URL.Query().Get("role")
	token := r.URL.Query().Get("token")
	if role != "server" && role != "client" {
		http.Error(w, "invalid role", http.StatusBadRequest)
		return
	}
	if len(token) < 16 {
		http.Error(w, "token too short", http.StatusBadRequest)
		return
	}

	var rm *room
	var ok bool
	if role == "server" {
		rm = h.getOrCreateServerRoom(token)
	} else {
		rm, ok = h.getOrLoadClientRoom(token)
		if !ok {
			http.Error(w, "room not found", http.StatusGone)
			return
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	rm.mu.Lock()

	if role == "server" && rm.server != nil {
		rm.mu.Unlock()
		conn.WriteJSON(relayMessage{Type: "error", Ciphertext: "server already connected"})
		conn.Close()
		return
	}

	p := &peer{
		hub:    h,
		room:   rm,
		role:   role,
		conn:   conn,
		sendCh: make(chan []byte, 10000),
		done:   make(chan struct{}),
	}

	if role == "server" {
		if rm.offlineTimer != nil {
			rm.offlineTimer.Stop()
			rm.offlineTimer = nil
		}
		rm.server = p
	} else {
		rm.clients[p] = struct{}{}
		rm.notifyServerClientConnected()
		h.traceRelayMessage("server_notify", token, "", relayMessage{
			Type:        "connected",
			Role:        "client",
			SessionID:   rm.sessionID,
			Count:       len(rm.history),
			LastEventID: lastEventID(rm.history),
		}, "reason=client_join")
	}
	clients := len(rm.clients)
	sessionID := rm.sessionID
	bufferedEvents := len(rm.history)
	rm.mu.Unlock()

	if h.stats != nil {
		h.stats.recordConnect(role)
	}
	log.Printf(
		"[relay] %s connected: room=%s session=%s clients=%d buffered_events=%d",
		role,
		shortToken(token),
		sessionID,
		clients,
		bufferedEvents,
	)
	go p.writePump()
	if role == "client" && rm.server == nil {
		h.scheduleRoomExpiry(token)
		p.sendJSON(serverOfflineNotice(sessionID))
		time.AfterFunc(500*time.Millisecond, func() {
			if conn != nil {
				_ = conn.Close()
			}
		})
	}
	p.readPump(h) // blocks
}

// ─── Main ───

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	store, err := openRelayStore(relayDBPath(), defaultCleanupAge)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if err := store.cleanupExpired(time.Now()); err != nil {
		log.Printf("[relay] initial cleanup failed: %v", err)
	}
	go func() {
		ticker := time.NewTicker(defaultCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			if err := store.cleanupExpired(time.Now()); err != nil {
				log.Printf("[relay] periodic cleanup failed: %v", err)
			}
		}
	}()
	h := newHub(store)
	go func() {
		ticker := time.NewTicker(traceFlushInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.flushTraceLogs()
		}
	}()
	go func() {
		ticker := time.NewTicker(defaultStatsInterval)
		defer ticker.Stop()
		for range ticker.C {
			h.logStats()
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/nuke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})
	log.Printf("[relay] listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
