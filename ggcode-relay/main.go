package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const peerWriteTimeout = 30 * time.Second

// relayMessage is the wire format. Metadata fields remain plaintext so relay
// can manage replay and ordering without decrypting business payloads.
type relayMessage struct {
	Type        string          `json:"type"`
	SessionID   string          `json:"session_id,omitempty"`
	EventID     string          `json:"event_id,omitempty"`
	StreamID    string          `json:"stream_id,omitempty"`
	ClientID    string          `json:"client_id,omitempty"`
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
	offlineTimer *time.Timer // grace period before notifying clients
}

func newRoom(token string) *room {
	return &room{
		token:       token,
		clients:     make(map[*peer]struct{}),
		clientsByID: make(map[string]*peer),
	}
}

func (r *room) notifyServerClientConnected() {
	if r.server == nil {
		return
	}
	r.server.sendJSON(relayMessage{
		Type:      "connected",
		Role:      "client",
		SessionID: r.sessionID,
		Count:     len(r.history),
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
	connState := relayMessage{
		Type:      "connected",
		Role:      p.role,
		SessionID: p.room.sessionID,
		Count:     len(p.room.history),
	}
	p.room.mu.RUnlock()
	connMsg, _ := json.Marshal(connState)
	_ = p.conn.SetWriteDeadline(time.Now().Add(peerWriteTimeout))
	if err := p.conn.WriteMessage(websocket.TextMessage, connMsg); err != nil {
		return
	}
	if connState.SessionID != "" {
		activeMsg, _ := json.Marshal(relayMessage{
			Type:      "active_session",
			SessionID: connState.SessionID,
			Data:      mustJSON(activeSessionData{SessionID: connState.SessionID}),
		})
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
	defer func() {
		close(p.done) // signal writePump to stop
		p.conn.Close()
		p.room.mu.Lock()
		if p.role == "server" {
			p.room.server = nil
			token := p.room.token
			clients := make([]*peer, 0, len(p.room.clients))
			for c := range p.room.clients {
				clients = append(clients, c)
			}
			// Cancel any pending offline timer
			if p.room.offlineTimer != nil {
				p.room.offlineTimer.Stop()
				p.room.offlineTimer = nil
			}
			p.room.offlineTimer = time.AfterFunc(30*time.Second, func() {
				p.room.mu.Lock()
				defer p.room.mu.Unlock()
				if p.room.server != nil {
					return // reconnected during grace period
				}
				log.Printf("[relay] server grace period expired: room=%s", token[:8])
				for _, c := range clients {
					c.sendJSON(relayMessage{Type: "server_offline"})
				}
				p.room.offlineTimer = nil
			})
			p.room.mu.Unlock()
			// Keep room alive for reconnection
			go func() {
				time.Sleep(5 * time.Minute)
				h.removeRoomIfEmpty(h.getOrCreateRoom(token))
			}()
		} else {
			delete(p.room.clients, p)
			if p.clientID != "" {
				if current := p.room.clientsByID[p.clientID]; current == p {
					delete(p.room.clientsByID, p.clientID)
				}
			}
			p.room.mu.Unlock()
		}
		log.Printf("[relay] %s disconnected: room=%s", p.role, p.room.token[:8])
	}()

	p.conn.SetReadLimit(1 << 20)
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
		return nil
	})

	for {
		p.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
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
		case "active_session":
			if p.role == "server" {
				p.handleActiveSession(msg)
			}
			continue
		case "resume_hello", "resume_from":
			if p.role == "client" {
				p.handleResume(msg)
			}
			continue
		case "language_change":
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
			// proceed
		default:
			continue
		}

		p.room.mu.Lock()
		var persistMsg relayMessage
		var persistRaw []byte
		if p.role == "server" {
			if msg.SessionID != "" && msg.SessionID != p.room.sessionID {
				p.room.sessionID = msg.SessionID
				p.room.history = nil
			}
			if msg.SessionID != "" {
				p.room.history = append(p.room.history, roomEvent{
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
					c.sendRaw(raw)
				}
			}
		} else {
			if p.room.server != nil {
				p.room.server.sendRaw(raw)
			}
		}
		p.room.mu.Unlock()
		if p.role == "server" && p.hub != nil && p.hub.store != nil && persistMsg.SessionID != "" {
			if err := p.hub.store.persistEvent(p.room.token, persistMsg, persistRaw); err != nil {
				log.Printf("[relay] persist event failed: room=%s err=%v", p.room.token[:8], err)
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
	p.room.mu.Lock()
	changed := p.room.sessionID != sessionID
	p.room.sessionID = sessionID
	if changed {
		p.room.history = nil
	}
	for c := range p.room.clients {
		clients = append(clients, c)
	}
	token := p.room.token
	p.room.mu.Unlock()

	active := relayMessage{
		Type:      "active_session",
		SessionID: sessionID,
		Data:      mustJSON(activeSessionData{SessionID: sessionID}),
	}
	for _, c := range clients {
		c.sendJSON(active)
	}
	if p.hub != nil && p.hub.store != nil {
		if err := p.hub.store.persistActiveSession(token, sessionID); err != nil {
			log.Printf("[relay] persist active session failed: room=%s err=%v", token[:8], err)
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

	switch mode {
	case "snapshot_required":
		p.sendJSON(relayMessage{Type: "resume_miss", SessionID: p.room.sessionID, ClientID: msg.ClientID})
		p.sendJSON(relayMessage{Type: "snapshot_reset", SessionID: p.room.sessionID, ClientID: msg.ClientID})
		for _, ev := range replay {
			p.sendRaw(ev.raw)
		}
	default:
		for _, ev := range replay {
			p.sendRaw(ev.raw)
		}
	}
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
	rooms map[string]*room
	store *relayStore
	mu    sync.RWMutex
}

func newHub(store *relayStore) *hub {
	return &hub{rooms: make(map[string]*room), store: store}
}

func (h *hub) getOrCreateRoom(token string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[token]; ok {
		return r
	}
	r := newRoom(token)
	if h.store != nil {
		state, err := h.store.loadRoom(token)
		if err != nil {
			log.Printf("[relay] load room failed: room=%s err=%v", token[:8], err)
		} else {
			r.sessionID = state.sessionID
			r.history = state.history
		}
	}
	h.rooms[token] = r
	return r
}

func (h *hub) removeRoomIfEmpty(r *room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r.mu.RLock()
	empty := r.server == nil && len(r.clients) == 0
	r.mu.RUnlock()
	if empty {
		delete(h.rooms, r.token)
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

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	rm := h.getOrCreateRoom(token)
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
		rm.server = p
	} else {
		rm.clients[p] = struct{}{}
		rm.notifyServerClientConnected()
	}
	rm.mu.Unlock()

	log.Printf("[relay] %s connected: room=%s clients=%d", role, token[:8], len(rm.clients))
	go p.writePump()
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
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	log.Printf("[relay] listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
