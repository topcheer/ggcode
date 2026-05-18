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
	Role       string `json:"role,omitempty"`  // for "connected"
	Count      int    `json:"count,omitempty"` // for "replay_start"
}

// ─── Room ───

type room struct {
	token   string
	server  *peer
	clients map[*peer]struct{}
	cache   [][]byte // encrypted messages from server, FIFO
	mu      sync.RWMutex
}

func newRoom(token string) *room {
	return &room{token: token, clients: make(map[*peer]struct{})}
}

type peer struct {
	room   *room
	role   string // "server" or "client"
	conn   *websocket.Conn
	sendCh chan []byte
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

// ─── Peer pumps ───

func (p *peer) writePump() {
	defer p.conn.Close()
	for msg := range p.sendCh {
		if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (p *peer) sendJSON(msg relayMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case p.sendCh <- data:
	default:
	}
}

func (p *peer) readPump(h *hub) {
	defer func() {
		p.conn.Close()
		p.room.mu.Lock()
		if p.role == "server" {
			p.room.server = nil
			// Notify all clients
			for c := range p.room.clients {
				c.sendJSON(relayMessage{Type: "server_offline"})
			}
			token := p.room.token
			p.room.mu.Unlock()
			// Delayed cleanup
			go func() {
				time.Sleep(5 * time.Minute)
				h.removeRoomIfEmpty(h.getOrCreateRoom(token))
			}()
		} else {
			delete(p.room.clients, p)
			p.room.mu.Unlock()
		}
		close(p.sendCh)
		log.Printf("[relay] %s disconnected: room=%s", p.role, p.room.token[:8])
	}()

	p.conn.SetReadLimit(1 << 20)
	p.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
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
			// Cache
			p.room.cache = append(p.room.cache, raw)
			if len(p.room.cache) > 10000 {
				p.room.cache = p.room.cache[len(p.room.cache)-10000:]
			}
			// Broadcast to all clients
			for c := range p.room.clients {
				select {
				case c.sendCh <- raw:
				default:
				}
			}
		} else {
			// Client → forward to server + broadcast to other clients
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

	p := &peer{room: rm, role: role, conn: conn, sendCh: make(chan []byte, 256)}

	if role == "server" {
		rm.server = p
	} else {
		rm.clients[p] = struct{}{}
		// Notify server
		if rm.server != nil {
			rm.server.sendJSON(relayMessage{Type: "client_joined"})
		}
	}

	cache := make([][]byte, len(rm.cache))
	copy(cache, rm.cache)
	rm.mu.Unlock()

	log.Printf("[relay] %s connected: room=%s clients=%d", role, token[:8], len(rm.clients))

	go p.writePump()

	// Send connected confirmation
	p.sendJSON(relayMessage{Type: "connected", Role: role})

	// Replay cached messages to new clients
	if role == "client" && len(cache) > 0 {
		p.sendJSON(relayMessage{Type: "replay_start", Count: len(cache)})
		for _, m := range cache {
			select {
			case p.sendCh <- m:
			default:
			}
		}
		p.sendJSON(relayMessage{Type: "replay_end"})
		log.Printf("[relay] replayed %d messages to client", len(cache))
	}

	p.readPump(h)
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
