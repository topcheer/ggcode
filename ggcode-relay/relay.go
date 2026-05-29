package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ─── Wire protocol ───

type relayMessage struct {
	Type         string          `json:"type"`
	SessionID    string          `json:"session_id,omitempty"`
	EventID      string          `json:"event_id,omitempty"`
	StreamID     string          `json:"stream_id,omitempty"`
	ClientID     string          `json:"client_id,omitempty"`
	MessageID    string          `json:"message_id,omitempty"`
	Role         string          `json:"role,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	RetryAfterMS int             `json:"retry_after_ms,omitempty"`
	ResumeMode   string          `json:"resume_mode,omitempty"`
	Count        int             `json:"count,omitempty"`
	LastEventID  string          `json:"last_event_id,omitempty"`
	Nonce        json.RawMessage `json:"nonce,omitempty"`
	Ciphertext   json.RawMessage `json:"ciphertext,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
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
	token       string
	sessionID   string
	history     []roomEvent
	server      *peer
	clients     map[*peer]struct{}
	clientsByID map[string]*peer
	lastEventAt time.Time

	mu           sync.RWMutex
	offlineTimer *time.Timer
}

func newRoom(token string) *room {
	return &room{
		token:       token,
		clients:     make(map[*peer]struct{}),
		clientsByID: make(map[string]*peer),
	}
}

func (r *room) appendEvent(ev roomEvent) {
	if ev.eventID == "" {
		return
	}
	// Deduplicate the tail (idempotent upsert for retries).
	for i := len(r.history) - 1; i >= 0 && i >= len(r.history)-50; i-- {
		if r.history[i].eventID == ev.eventID {
			r.history[i] = ev
			return
		}
	}
	r.history = append(r.history, ev)
	r.lastEventAt = time.Now()
}

func (r *room) eventsAfter(cursor string) []roomEvent {
	if cursor == "" {
		out := make([]roomEvent, len(r.history))
		copy(out, r.history)
		return out
	}
	for i, ev := range r.history {
		if ev.eventID == cursor {
			out := make([]roomEvent, len(r.history)-i-1)
			copy(out, r.history[i+1:])
			return out
		}
	}
	// Cursor not found in history — send everything.
	out := make([]roomEvent, len(r.history))
	copy(out, r.history)
	return out
}

// ─── Peer ───

type peer struct {
	hub      *hub
	room     *room
	role     string // "server" or "client"
	conn     *websocket.Conn
	sendCh   chan []byte
	done     chan struct{}
	clientID string
	ready    bool
	cursor   string // relay-authoritative ACK cursor
}

func newPeer(h *hub, room *room, role string, conn *websocket.Conn) *peer {
	return &peer{
		hub:    h,
		room:   room,
		role:   role,
		conn:   conn,
		sendCh: make(chan []byte, 10000),
		done:   make(chan struct{}),
	}
}

// send enqueues a message. Blocks if the send buffer is full (back-pressure).
func (p *peer) send(msg relayMessage) {
	data, _ := json.Marshal(msg)
	p.sendRaw(data)
}

func (p *peer) sendRaw(raw []byte) {
	select {
	case p.sendCh <- raw:
	case <-p.done:
	}
}

// writeLoop drains sendCh and writes to the WebSocket.
func (p *peer) writeLoop() {
	defer p.conn.Close()
	for {
		select {
		case raw, ok := <-p.sendCh:
			if !ok {
				return
			}
			_ = p.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err := p.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-p.done:
			return
		}
	}
}

// readLoop reads from WebSocket and dispatches messages.
func (p *peer) readLoop(h *hub) {
	roomDestroyed := false
	defer func() {
		close(p.done)
		p.conn.Close()
		p.detachFromRoom(roomDestroyed, h)
	}()

	p.conn.SetReadLimit(1 << 20)
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})
	p.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	for {
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		p.conn.SetReadDeadline(time.Now().Add(120 * time.Second))

		var msg relayMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}

		switch msg.Type {
		case "encrypted":
			p.onEncrypted(raw, msg)
		case "active_session":
			p.onActiveSession(msg)
		case "stop_sharing":
			if p.role == "server" {
				h.trace("server_request", p.room.token, msg)
				roomDestroyed = true
				h.destroyRoom(p.room.token)
			}
		case "resume_hello", "resume_from":
			if p.role == "client" {
				h.trace("client_request", p.room.token, msg)
				p.onResume(msg, h)
			}
		case "event_ack":
			if p.role == "client" {
				p.onAck(msg, h)
			}
		case "language_change":
			p.relayToOthers(msg)
		case "ping":
			p.send(relayMessage{Type: "pong"})
		}
	}
}

func (p *peer) detachFromRoom(roomDestroyed bool, h *hub) {
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
			if p.room.clientsByID[p.clientID] == p {
				delete(p.room.clientsByID, p.clientID)
			}
		}
		p.room.mu.Unlock()
		h.removeRoomIfEmpty(p.room)
	}
	if p.hub != nil && p.hub.stats != nil {
		p.hub.stats.recordDisconnect(p.role)
	}
	log.Printf("[relay] %s disconnected: room=%s client=%s",
		p.role, shortToken(p.room.token), p.clientID)
}

// ─── Message handlers ───

func (p *peer) onEncrypted(raw []byte, msg relayMessage) {
	if p.role == "server" {
		p.handleServerBroadcast(raw, msg)
	} else {
		// Client → Server (user input).
		p.room.mu.RLock()
		srv := p.room.server
		p.room.mu.RUnlock()
		if srv != nil {
			srv.sendRaw(raw)
		}
	}
}

func (p *peer) handleServerBroadcast(raw []byte, msg relayMessage) {
	p.room.mu.Lock()
	if msg.SessionID != "" && msg.SessionID != p.room.sessionID {
		p.room.sessionID = msg.SessionID
		p.room.history = nil
		// Load new session history from DB.
		if p.hub.store != nil {
			events, _ := p.hub.store.loadSessionHistory(msg.SessionID)
			p.room.history = events
			p.room.lastEventAt = time.Now()
			if p.hub.stats != nil {
				p.hub.stats.recordActiveSession(true, len(events))
			}
		}
	}
	ev := roomEvent{
		sessionID: msg.SessionID,
		eventID:   msg.EventID,
		streamID:  msg.StreamID,
		typ:       "encrypted",
		raw:       append([]byte(nil), raw...),
	}
	p.room.appendEvent(ev)

	deliveries := 0
	for c := range p.room.clients {
		if c.ready {
			deliveries++
			c.sendRaw(raw)
		}
	}
	p.room.mu.Unlock()

	p.hub.trace("server_broadcast", p.room.token, msg)

	// Persist async.
	if p.hub.store != nil && msg.SessionID != "" {
		token := p.room.token
		s := p.hub.store
		go func() {
			if err := s.persistEvent(token, msg, append([]byte(nil), raw...)); err != nil {
				log.Printf("[relay] persist error: %v", err)
			}
		}()
	}
}

func (p *peer) onActiveSession(msg relayMessage) {
	if p.role != "server" {
		return
	}
	p.hub.trace("server_request", p.room.token, msg)

	sessionID := msg.SessionID
	if sessionID == "" {
		var data struct {
			SessionID string `json:"session_id"`
		}
		if msg.Data != nil {
			_ = json.Unmarshal(msg.Data, &data)
		}
		sessionID = data.SessionID
	}
	if sessionID == "" {
		return
	}

	p.room.mu.Lock()
	changed := p.room.sessionID != sessionID
	p.room.sessionID = sessionID
	if changed {
		p.room.history = nil
	}
	if len(p.room.history) == 0 && p.hub.store != nil {
		events, _ := p.hub.store.loadSessionHistory(sessionID)
		p.room.history = events
		p.room.lastEventAt = time.Now()
		log.Printf("[relay] hydrate room=%s session=%s events=%d",
			shortToken(p.room.token), sessionID, len(events))
		if p.hub.stats != nil {
			p.hub.stats.recordActiveSession(changed, len(events))
		}
	}
	for c := range p.room.clients {
		c.send(msg)
	}
	p.room.mu.Unlock()

	p.hub.trace("relay_push", p.room.token, msg)

	if p.hub.store != nil {
		go func() {
			_ = p.hub.store.persistActiveSession(p.room.token, sessionID)
		}()
	}
}

func (p *peer) onResume(msg relayMessage, h *hub) {
	if msg.ClientID == "" {
		p.send(relayMessage{Type: "error", Reason: "missing client_id"})
		return
	}

	p.room.mu.Lock()
	defer p.room.mu.Unlock()

	p.clientID = msg.ClientID
	p.room.clientsByID[msg.ClientID] = p

	// Relay is the cursor authority — load from DB if not in memory.
	if p.cursor == "" && h.store != nil {
		cursor, err := h.store.loadClientCursor(hashToken(p.room.token), msg.ClientID)
		if err != nil {
			log.Printf("[relay] cursor load error: %v", err)
		}
		p.cursor = cursor
	}

	replay := p.room.eventsAfter(p.cursor)
	mode := "incremental"
	if p.cursor == "" {
		mode = "full_history"
	}

	// 1. Send active_session so mobile can load its cached snapshot.
	p.send(relayMessage{
		Type:      "active_session",
		SessionID: p.room.sessionID,
		ClientID:  msg.ClientID,
	})

	// 2. Send resume_ack.
	p.send(relayMessage{
		Type:      "resume_ack",
		SessionID: p.room.sessionID,
		ClientID:  msg.ClientID,
		Data:      mustJSON(map[string]interface{}{"resume_mode": mode, "replay_count": len(replay)}),
	})

	for _, ev := range replay {
		h.traceRoomEvent("replay_send", p.room.token, p.clientID, ev, "mode="+mode)
		p.sendRaw(ev.raw)
	}

	p.ready = true

	if h.stats != nil {
		h.stats.recordResume(mode, len(replay))
	}
	log.Printf("[relay] resume room=%s client=%s cursor=%s mode=%s replay=%d",
		shortToken(p.room.token), msg.ClientID, p.cursor, mode, len(replay))
}

func (p *peer) onAck(msg relayMessage, h *hub) {
	if msg.EventID == "" {
		return
	}
	p.room.mu.Lock()
	p.cursor = msg.EventID
	p.room.mu.Unlock()

	if h.store != nil {
		th := hashToken(p.room.token)
		sid := p.room.sessionID
		s := h.store
		cid := p.clientID
		eid := msg.EventID
		go func() {
			if err := s.saveClientCursor(th, cid, sid, eid); err != nil {
				log.Printf("[relay] cursor save error: %v", err)
			}
		}()
	}
}

func (p *peer) relayToOthers(msg relayMessage) {
	p.room.mu.Lock()
	defer p.room.mu.Unlock()
	for c := range p.room.clients {
		if c != p {
			c.send(msg)
		}
	}
	if p.room.server != nil && p.room.server != p {
		p.room.server.send(msg)
	}
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

func (h *hub) getOrCreateRoom(token string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[token]; ok {
		return r
	}
	r := newRoom(token)
	h.rooms[token] = r
	return r
}

func (h *hub) removeRoomIfEmpty(r *room) {
	h.mu.Lock()
	current, ok := h.rooms[r.token]
	if !ok || current != r {
		h.mu.Unlock()
		return
	}
	r.mu.Lock()
	empty := r.server == nil && len(r.clients) == 0
	if !empty {
		r.mu.Unlock()
		h.mu.Unlock()
		return
	}
	stopOfflineTimerLocked(r)
	delete(h.rooms, r.token)
	r.mu.Unlock()
	h.mu.Unlock()

	if h.store != nil {
		go func() { _ = h.store.destroyRoom(r.token) }()
	}
	if h.stats != nil {
		h.stats.recordRoomDestroy()
	}
	log.Printf("[relay] empty room destroyed: room=%s", shortToken(r.token))
}

func (h *hub) notifyRoomRecovering(token, sessionID string) {
	h.mu.RLock()
	r := h.rooms[token]
	h.mu.RUnlock()
	if r == nil {
		return
	}
	r.mu.RLock()
	srv := r.server
	clients := len(r.clients)
	r.mu.RUnlock()
	if srv != nil {
		srv.send(relayMessage{
			Type:      "connected",
			Role:      "client",
			SessionID: sessionID,
			Reason:    "recovering",
		})
	}
	log.Printf("[relay] server offline: room=%s clients=%d", shortToken(token), clients)
}

func (h *hub) scheduleRoomExpiry(token string) {
	h.mu.Lock()
	r := h.rooms[token]
	if r == nil {
		h.mu.Unlock()
		return
	}
	r.mu.Lock()
	stopOfflineTimerLocked(r)
	r.offlineTimer = time.AfterFunc(5*time.Minute, func() {
		h.expireRoom(token)
	})
	r.mu.Unlock()
	h.mu.Unlock()
}

func (h *hub) expireRoom(token string) {
	h.mu.Lock()
	r, ok := h.rooms[token]
	if !ok {
		h.mu.Unlock()
		return
	}
	r.mu.Lock()
	hasServer := r.server != nil
	if hasServer {
		r.mu.Unlock()
		h.mu.Unlock()
		return
	}
	stopOfflineTimerLocked(r)
	delete(h.rooms, token)
	r.mu.Unlock()
	h.mu.Unlock()

	notice := relayMessage{Type: "sharing_stopped"}
	r.mu.RLock()
	for c := range r.clients {
		c.send(notice)
	}
	r.mu.RUnlock()

	if h.store != nil {
		go func() { _ = h.store.destroyRoom(token) }()
	}
	if h.stats != nil {
		h.stats.recordRoomDestroy()
	}
	log.Printf("[relay] room expired: room=%s", shortToken(token))
}

func (h *hub) evictStaleRooms(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	h.mu.RLock()
	var stale []string
	for token, r := range h.rooms {
		r.mu.RLock()
		if !r.lastEventAt.IsZero() && r.lastEventAt.Before(cutoff) {
			stale = append(stale, token)
		}
		r.mu.RUnlock()
	}
	h.mu.RUnlock()
	for _, token := range stale {
		h.expireRoom(token)
	}
	if len(stale) > 0 {
		log.Printf("[relay] evicted %d stale rooms (no events for %s)", len(stale), maxAge)
	}
}

func (h *hub) destroyRoom(token string) {
	h.mu.Lock()
	r, ok := h.rooms[token]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(h.rooms, token)
	h.mu.Unlock()

	notice := relayMessage{Type: "sharing_stopped"}
	r.mu.Lock()
	stopOfflineTimerLocked(r)
	for c := range r.clients {
		c.send(notice)
	}
	srv := r.server
	r.server = nil
	r.mu.Unlock()

	if srv != nil {
		srv.send(notice)
	}
	if h.store != nil {
		go func() { _ = h.store.destroyRoom(token) }()
	}
	if h.stats != nil {
		h.stats.recordRoomDestroy()
	}
	log.Printf("[relay] room destroyed: room=%s", shortToken(token))
}

func stopOfflineTimerLocked(r *room) {
	if r.offlineTimer != nil {
		r.offlineTimer.Stop()
		r.offlineTimer = nil
	}
}

// trace is a convenience wrapper.
func (h *hub) trace(route, roomToken string, msg relayMessage) {
	if h.tracer != nil {
		h.tracer.Log(route, traceMessageSummary(route, roomToken, "", msg, ""))
	}
}

// ─── WebSocket handler ───

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func (h *hub) handleShareSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	issued, err := issueShareSession(loadShareAuthConfig())
	if err != nil {
		if err.Error() == "share v2 unavailable" {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[relay] issued share session: room=%s", shortToken(issued.RoomID))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(issued)
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	handshake, status, reason := validateShareHandshake(r, loadShareAuthConfig())
	if handshake == nil {
		http.Error(w, reason, status)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	token := handshake.roomKey
	role := handshake.role
	clientID := r.URL.Query().Get("client_id")

	room := h.getOrCreateRoom(token)
	p := newPeer(h, room, role, conn)
	p.clientID = clientID

	room.mu.Lock()
	if role == "server" {
		if room.server != nil {
			// Kick old server.
			old := room.server
			room.server = nil
			room.mu.Unlock()
			old.send(relayMessage{Type: "sharing_stopped"})
			room.mu.Lock()
		}
		room.server = p
		stopOfflineTimerLocked(room)
	} else {
		room.clients[p] = struct{}{}
	}
	clients := len(room.clients)
	room.mu.Unlock()

	if h.stats != nil {
		h.stats.recordConnect(role)
	}
	log.Printf("[relay] %s connected: room=%s session=%s clients=%d buffered=%d",
		role, shortToken(token), room.sessionID, clients, len(room.history))

	// Send initial "connected" with current tail.
	room.mu.RLock()
	tail := ""
	if n := len(room.history); n > 0 {
		tail = room.history[n-1].eventID
	}
	room.mu.RUnlock()
	p.send(relayMessage{
		Type:        "connected",
		SessionID:   room.sessionID,
		LastEventID: tail,
		Data:        mustJSON(connectedShareMetadata(handshake)),
	})

	// Notify server that a client connected.
	if role == "client" {
		room.mu.Lock()
		room.notifyServerClientConnected()
		room.mu.Unlock()
	}

	go p.writeLoop()
	p.readLoop(h) // blocks until disconnect
}

func (r *room) notifyServerClientConnected() {
	if r.server == nil {
		return
	}
	tail := ""
	if n := len(r.history); n > 0 {
		tail = r.history[n-1].eventID
	}
	r.server.send(relayMessage{
		Type:        "connected",
		Role:        "client",
		SessionID:   r.sessionID,
		Count:       len(r.history),
		LastEventID: tail,
	})
}

// ─── Main ───

const relayAdminTokenEnv = "GGCODE_RELAY_ADMIN_TOKEN"

func mustJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func constantTimeMatch(provided, expected string) bool {
	if provided == "" || expected == "" {
		return false
	}
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func relayAdminAuthorized(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		return false
	}
	token := strings.TrimSpace(r.Header.Get("X-GGCode-Admin-Token"))
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[7:])
		}
	}
	return constantTimeMatch(token, expectedToken)
}

func newNukeHandler(store *relayStore, h *hub, adminToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		if adminToken == "" {
			http.Error(w, "nuke disabled", http.StatusServiceUnavailable)
			return
		}
		if !relayAdminAuthorized(r, adminToken) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ggcode-relay"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := store.nukeAll(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		h.mu.Lock()
		for token, r := range h.rooms {
			r.mu.Lock()
			stopOfflineTimerLocked(r)
			for c := range r.clients {
				c.send(relayMessage{Type: "sharing_stopped"})
			}
			if r.server != nil {
				r.server.send(relayMessage{Type: "sharing_stopped"})
			}
			r.mu.Unlock()
			delete(h.rooms, token)
		}
		h.mu.Unlock()
		w.WriteHeader(200)
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := relayDBPath()
	store, err := openRelayStore(dbPath, 72*time.Hour)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	h := newHub(store)
	adminToken := strings.TrimSpace(os.Getenv(relayAdminTokenEnv))

	mux := http.NewServeMux()
	mux.HandleFunc("/share/session", h.handleShareSession)
	mux.HandleFunc("/ws", h.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		snap, err := h.stats.snapshot(h)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap)
	})
	if adminToken == "" {
		log.Printf("[relay] /nuke disabled: %s is not set", relayAdminTokenEnv)
	}
	mux.HandleFunc("/nuke", newNukeHandler(store, h, adminToken))

	// Background tasks.
	go func() {
		for range time.Tick(10 * time.Second) {
			h.logStats()
			h.flushTraceLogs()
		}
	}()
	go func() {
		for range time.Tick(time.Hour) {
			_ = store.cleanupExpired(time.Now())
		}
	}()
	go func() {
		for range time.Tick(time.Hour) {
			h.evictStaleRooms(12 * time.Hour)
		}
	}()

	log.Printf("[relay] listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
