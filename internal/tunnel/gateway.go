package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Gateway is a WebSocket server with token-based authentication.
// It provides a simple bidirectional JSON message pipe.
//
// Mobile client connects via:
//
//	wss://<tunnel-url>/ws?token=<token>
//
// Messages are JSON: {"type":"...","data":{...}}
type Gateway struct {
	port      int
	token     string
	server    *http.Server
	upgrader  websocket.Upgrader
	onMessage func(msg GatewayMessage) // called when client sends a message

	mu     sync.RWMutex
	conn   *websocket.Conn
	connMu sync.Mutex
	done   chan struct{}
}

// GatewayMessage is a JSON message exchanged over the WebSocket.
type GatewayMessage struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// NewGateway creates a gateway on a random port with a random token.
func NewGateway() *Gateway {
	token := generateToken(24)
	return &Gateway{
		token: token,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		done: make(chan struct{}),
	}
}

// Start starts the WebSocket server. Returns (port, token, error).
func (g *Gateway) Start() (int, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, "", fmt.Errorf("gateway listen: %w", err)
	}
	g.port = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", g.handleWS)
	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	g.server = &http.Server{Handler: mux}
	go func() {
		if err := g.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway server error: %v", err)
		}
	}()

	return g.port, g.token, nil
}

// Port returns the local port the gateway is listening on.
func (g *Gateway) Port() int { return g.port }

// Token returns the authentication token.
func (g *Gateway) Token() string { return g.token }

// OnMessage sets the handler for incoming messages from the client.
func (g *Gateway) OnMessage(fn func(msg GatewayMessage)) {
	g.onMessage = fn
}

// Send sends a message to the connected client.
func (g *Gateway) Send(msg GatewayMessage) error {
	g.connMu.Lock()
	defer g.connMu.Unlock()
	if g.conn == nil {
		return fmt.Errorf("no client connected")
	}
	return g.conn.WriteJSON(msg)
}

// Close shuts down the gateway.
func (g *Gateway) Close() error {
	if g.server != nil {
		g.server.Close()
	}
	return nil
}

func (g *Gateway) handleWS(w http.ResponseWriter, r *http.Request) {
	// Token validation
	token := r.URL.Query().Get("token")
	if token != g.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("gateway ws upgrade error: %v", err)
		return
	}

	g.mu.Lock()
	g.conn = conn
	g.mu.Unlock()

	// Read loop
	go func() {
		defer func() {
			g.connMu.Lock()
			if g.conn == conn {
				g.conn = nil
			}
			g.connMu.Unlock()
			conn.Close()
		}()

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				if err != io.EOF && websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
					log.Printf("gateway ws read error: %v", err)
				}
				return
			}

			var msg GatewayMessage
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				log.Printf("gateway ws invalid message: %v", err)
				continue
			}

			if g.onMessage != nil {
				g.onMessage(msg)
			}
		}
	}()
}

// ConnectURL returns the full WebSocket URL for a given tunnel host.
func (g *Gateway) ConnectURL(tunnelHost string) string {
	return fmt.Sprintf("wss://%s/ws?token=%s", tunnelHost, g.token)
}

func generateToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
