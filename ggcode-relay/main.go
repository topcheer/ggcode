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

// relayMessage is the wire format — gateway only sees type + opaque blobs.
type relayMessage struct {
	Type       string `json:"type"`
	Nonce      string `json:"nonce,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
	Role       string `json:"role,omitempty"`
	Count      int    `json:"count,omitempty"`
}

// ─── Room ───

type room struct {
	token   string
	server  *peer
	clients map[*peer]struct{}
	cache   [][]byte
	mu      sync.RWMutex
}

func newRoom(token string) *room {
	return &room{token: token, clients: make(map[*peer]struct{})}
}

// ─── Peer ───
//
// writePump is the ONLY goroutine that writes to conn.
// It sends: connected → replay (clients only) → live messages from sendCh.
// This guarantees strict FIFO ordering — replay and live broadcasts never interleave.

type peer struct {
	room       *room
	role       string // "server" or "client"
	conn       *websocket.Conn
	sendCh     chan []byte // never closed; done channel signals stop
	done       chan struct{}
	replayData [][]byte // set once before writePump starts
}

// sendJSON enqueues a message for sending. Never panics — sendCh is never closed.
func (p *peer) sendJSON(msg relayMessage) {
	data, _ := json.Marshal(msg)
	select {
	case p.sendCh <- data:
	default:
	}
}

func (p *peer) writePump() {
	defer p.conn.Close()

	// 1. Send connected confirmation directly
	connMsg, _ := json.Marshal(relayMessage{Type: "connected", Role: p.role})
	if err := p.conn.WriteMessage(websocket.TextMessage, connMsg); err != nil {
		return
	}

	// 2. Replay cached messages to clients (serialized before live messages)
	if p.role == "client" && len(p.replayData) > 0 {
		startMsg, _ := json.Marshal(relayMessage{Type: "replay_start", Count: len(p.replayData)})
		if err := p.conn.WriteMessage(websocket.TextMessage, startMsg); err != nil {
			return
		}
		for _, m := range p.replayData {
			if err := p.conn.WriteMessage(websocket.TextMessage, m); err != nil {
				return
			}
		}
		endMsg, _ := json.Marshal(relayMessage{Type: "replay_end"})
		if err := p.conn.WriteMessage(websocket.TextMessage, endMsg); err != nil {
			return
		}
		log.Printf("[relay] replayed %d messages to client", len(p.replayData))
	}

	// 3. Normal pump — live messages from sendCh
	for {
		select {
		case msg := <-p.sendCh:
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
			for c := range p.room.clients {
				c.sendJSON(relayMessage{Type: "server_offline"})
			}
			token := p.room.token
			p.room.mu.Unlock()
			go func() {
				time.Sleep(5 * time.Minute)
				h.removeRoomIfEmpty(h.getOrCreateRoom(token))
			}()
		} else {
			delete(p.room.clients, p)
			p.room.mu.Unlock()
		}
		log.Printf("[relay] %s disconnected: room=%s", p.role, p.room.token[:8])
	}()

	p.conn.SetReadLimit(1 << 20)
	p.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
		return nil
	})

	for {
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
		case "encrypted":
			// proceed
		default:
			continue
		}

		p.room.mu.Lock()
		if p.role == "server" {
			p.room.cache = append(p.room.cache, raw)
			if len(p.room.cache) > 10000 {
				p.room.cache = p.room.cache[len(p.room.cache)-10000:]
			}
			for c := range p.room.clients {
				select {
				case c.sendCh <- raw:
				default:
				}
			}
		} else {
			if p.room.server != nil {
				select {
				case p.room.server.sendCh <- raw:
				default:
				}
			}
			for c := range p.room.clients {
				if c != p {
					select {
					case c.sendCh <- raw:
					default:
					}
				}
			}
		}
		p.room.mu.Unlock()
	}
}

// ─── Hub ───

type hub struct {
	rooms map[string]*room
	mu    sync.RWMutex
}

func newHub() *hub {
	return &hub{rooms: make(map[string]*room)}
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
		if rm.server != nil {
			rm.server.sendJSON(relayMessage{Type: "client_joined"})
		}
		// Copy cache atomically (inside room lock)
		p.replayData = make([][]byte, len(rm.cache))
		copy(p.replayData, rm.cache)
	}
	rm.mu.Unlock()

	log.Printf("[relay] %s connected: room=%s clients=%d", role, token[:8], len(rm.clients))
	go p.writePump() // writePump sends connected + replay + live messages
	p.readPump(h)    // blocks
}

// ─── Main ───

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	h := newHub()
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
