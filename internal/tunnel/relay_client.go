package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// RelayClient connects to the ggcode-relay server as the "server" role.
// It replaces the old Gateway + Tunnel combination.
type RelayClient struct {
	relayURL string // e.g. "wss://relay.ggcode.app"
	token    string
	crypto   *Crypto

	conn   *websocket.Conn
	connMu sync.Mutex
	sendCh chan []byte
	done   chan struct{}

	onMessage func(msg GatewayMessage)
	onConnect func() // called when a client joins

	mu sync.RWMutex
}

// NewRelayClient creates a client that will connect to the relay server.
func NewRelayClient(relayURL, token string) (*RelayClient, error) {
	crypto, err := NewCrypto(token)
	if err != nil {
		return nil, err
	}
	return &RelayClient{
		relayURL: relayURL,
		token:    token,
		crypto:   crypto,
		sendCh:   make(chan []byte, 256),
		done:     make(chan struct{}),
	}, nil
}

// Connect establishes the WebSocket connection to the relay server.
func (rc *RelayClient) Connect() error {
	url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)
	header := http.Header{}
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		return fmt.Errorf("relay dial: %w", err)
	}
	rc.conn = conn

	go rc.writePump()
	go rc.readPump()
	go rc.heartbeatLoop()

	log.Printf("[relay-client] connected to %s", rc.relayURL)
	return nil
}

func (rc *RelayClient) writePump() {
	defer rc.conn.Close()
	for msg := range rc.sendCh {
		rc.connMu.Lock()
		err := rc.conn.WriteMessage(websocket.TextMessage, msg)
		rc.connMu.Unlock()
		if err != nil {
			return
		}
	}
}

func (rc *RelayClient) readPump() {
	defer close(rc.done)

	rc.conn.SetReadLimit(1 << 20)
	rc.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	rc.conn.SetPongHandler(func(string) error {
		rc.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		_, raw, err := rc.conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				log.Printf("[relay-client] read error: %v", err)
			}
			return
		}

		var relayMsg struct {
			Type       string `json:"type"`
			Nonce      string `json:"nonce,omitempty"`
			Ciphertext string `json:"ciphertext,omitempty"`
			Role       string `json:"role,omitempty"`
			Count      int    `json:"count,omitempty"`
		}
		if json.Unmarshal(raw, &relayMsg) != nil {
			continue
		}

		switch relayMsg.Type {
		case "connected":
			log.Printf("[relay-client] confirmed as %s", relayMsg.Role)

		case "client_joined":
			log.Printf("[relay-client] mobile client joined")
			rc.mu.RLock()
			fn := rc.onConnect
			rc.mu.RUnlock()
			if fn != nil {
				fn()
			}

		case "pong":
			// keepalive response

		case "encrypted":
			// Decrypt and dispatch
			plaintext, err := rc.crypto.Decrypt(relayMsg.Nonce, relayMsg.Ciphertext)
			if err != nil {
				log.Printf("[relay-client] decrypt error: %v", err)
				continue
			}
			var msg GatewayMessage
			if json.Unmarshal(plaintext, &msg) != nil {
				continue
			}
			rc.mu.RLock()
			fn := rc.onMessage
			rc.mu.RUnlock()
			if fn != nil {
				fn(msg)
			}
		}
	}
}

// Send encrypts and sends a GatewayMessage to the relay.
func (rc *RelayClient) Send(msg GatewayMessage) error {
	plaintext, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	nonce, ciphertext, err := rc.crypto.Encrypt(plaintext)
	if err != nil {
		return err
	}

	relayMsg := map[string]string{
		"type":       "encrypted",
		"nonce":      nonce,
		"ciphertext": ciphertext,
	}
	data, err := json.Marshal(relayMsg)
	if err != nil {
		return err
	}

	select {
	case rc.sendCh <- data:
		return nil
	default:
		return fmt.Errorf("send channel full")
	}
}

// OnMessage sets the handler for decrypted messages from mobile clients.
func (rc *RelayClient) OnMessage(fn func(msg GatewayMessage)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onMessage = fn
}

// OnConnect sets the handler called when a mobile client joins.
func (rc *RelayClient) OnConnect(fn func()) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onConnect = fn
}

// Close shuts down the client.
// heartbeatLoop sends ping every 30 seconds to keep the connection alive.
func (rc *RelayClient) heartbeatLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
	for {
		select {
		case <-ticker.C:
			select {
			case rc.sendCh <- pingMsg:
			default:
			}
		case <-rc.done:
			return
		}
	}
}

func (rc *RelayClient) Close() {
	close(rc.sendCh)
}

// ConnectURL returns the URL that mobile clients should use to connect.
func (rc *RelayClient) ConnectURL() string {
	return fmt.Sprintf("%s/ws?role=client&token=%s", rc.relayURL, rc.token)
}

// Token returns the session token.
func (rc *RelayClient) Token() string {
	return rc.token
}
