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
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Gateway is a WebSocket server with token-based authentication.
type Gateway struct {
	port      int
	token     string
	server    *http.Server
	upgrader  websocket.Upgrader
	onMessage func(msg GatewayMessage)

	mu     sync.RWMutex
	conn   *websocket.Conn
	connMu sync.Mutex
	done   chan struct{}

	// Client keepalive: track last message received from client.
	lastRecv   time.Time
	lastRecvMu sync.Mutex
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

func (g *Gateway) Port() int     { return g.port }
func (g *Gateway) Token() string { return g.token }

func (g *Gateway) OnMessage(fn func(msg GatewayMessage)) {
	g.onMessage = fn
}

func (g *Gateway) Send(msg GatewayMessage) error {
	g.connMu.Lock()
	defer g.connMu.Unlock()
	if g.conn == nil {
		return fmt.Errorf("no client connected")
	}
	return g.conn.WriteJSON(msg)
}

func (g *Gateway) Close() error {
	if g.server != nil {
		g.server.Close()
	}
	return nil
}

// LastRecv returns the time of the last message received from the client.
func (g *Gateway) LastRecv() time.Time {
	g.lastRecvMu.Lock()
	defer g.lastRecvMu.Unlock()
	return g.lastRecv
}

func (g *Gateway) handleWS(w http.ResponseWriter, r *http.Request) {
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

	log.Printf("[gateway] client connected from %s", conn.RemoteAddr())
	_ = os.WriteFile("/tmp/ggcode-gateway.log", []byte(fmt.Sprintf("connected: %s\n", conn.RemoteAddr())), 0644)

	g.mu.Lock()
	g.conn = conn
	g.mu.Unlock()

	g.lastRecvMu.Lock()
	g.lastRecv = time.Now()
	g.lastRecvMu.Unlock()

	// Read loop
	go func() {
		defer func() {
			g.connMu.Lock()
			if g.conn == conn {
				g.conn = nil
			}
			g.connMu.Unlock()
			conn.Close()
			log.Printf("[gateway] client disconnected")
			_ = os.WriteFile("/tmp/ggcode-gateway.log", []byte("disconnected\n"), 0644)
		}()

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				if err != io.EOF && !websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
					log.Printf("[gateway] ws read error: %v", err)
				}
				return
			}

			var msg GatewayMessage
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				log.Printf("[gateway] invalid message: %v", err)
				continue
			}

			// Update last received time (any message counts as alive)
			g.lastRecvMu.Lock()
			g.lastRecv = time.Now()
			g.lastRecvMu.Unlock()

			// Ping is just keepalive, no need to dispatch
			if msg.Type == "ping" {
				continue
			}

			log.Printf("[gateway] received from client: type=%s", msg.Type)
			_ = os.WriteFile("/tmp/ggcode-gateway.log", []byte(fmt.Sprintf("recv: type=%s\n", msg.Type)), 0644)

			if g.onMessage != nil {
				g.onMessage(msg)
			}
		}
	}()
}

func (g *Gateway) ConnectURL(tunnelHost string) string {
	return fmt.Sprintf("wss://%s/ws?token=%s", tunnelHost, g.token)
}

func generateToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
