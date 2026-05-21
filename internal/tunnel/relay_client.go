package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/debug"
)

// RelayClient connects to the ggcode-relay server as the "server" role.
// It auto-reconnects on disconnect with exponential backoff.
type RelayClient struct {
	relayURL string
	token    string
	crypto   *Crypto

	conn    *websocket.Conn
	connMu  sync.Mutex
	sendCh  chan []byte
	closed  bool
	closeMu sync.Mutex
	stopCh  chan struct{}

	onMessage func(msg GatewayMessage)
	mu        sync.RWMutex
}

func NewRelayClient(relayURL, token string) (*RelayClient, error) {
	crypto, err := NewCrypto(token)
	if err != nil {
		return nil, err
	}
	return &RelayClient{
		relayURL: strings.TrimSuffix(relayURL, "/"),
		token:    token,
		crypto:   crypto,
		sendCh:   make(chan []byte, 256),
		stopCh:   make(chan struct{}),
	}, nil
}

// Connect starts the connection loop. It connects, runs pumps, and auto-reconnects.
func (rc *RelayClient) Connect() error {
	if err := rc.dial(); err != nil {
		return err
	}
	go rc.run()
	return nil
}

func (rc *RelayClient) dial() error {
	url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		return fmt.Errorf("relay dial: %w", err)
	}
	rc.conn = conn
	return nil
}

func (rc *RelayClient) run() {
	for {
		done := make(chan struct{})
		var once sync.Once
		closeDone := func() { once.Do(func() { close(done) }) }

		go rc.writePump(closeDone)
		go rc.readPump(closeDone)

		<-done // Wait for either pump to exit
		rc.conn.Close()

		rc.closeMu.Lock()
		if rc.closed {
			rc.closeMu.Unlock()
			return
		}
		rc.closeMu.Unlock()

		// Reconnect with backoff
		debug.Log("tunnel", "relay-client: disconnected, reconnecting...")
		for attempt := 0; ; attempt++ {
			rc.closeMu.Lock()
			if rc.closed {
				rc.closeMu.Unlock()
				return
			}
			rc.closeMu.Unlock()

			if err := rc.dial(); err != nil {
				backoff := time.Duration(attempt+1) * time.Second
				if backoff > 10*time.Second {
					backoff = 10 * time.Second
				}
				debug.Log("tunnel", "relay-client: reconnect failed (attempt %d): %v, retry in %v", attempt+1, err, backoff)
				select {
				case <-time.After(backoff):
					continue
				case <-rc.stopCh:
					return
				}
			}
			debug.Log("tunnel", "relay-client: reconnected")
			break
		}
	}
}

func (rc *RelayClient) writePump(done func()) {
	defer done()
	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-rc.sendCh:
			if !ok {
				return
			}
			rc.connMu.Lock()
			err := rc.conn.WriteMessage(websocket.TextMessage, msg)
			rc.connMu.Unlock()
			if err != nil {
				return
			}
		case <-ticker.C:
			rc.connMu.Lock()
			err := rc.conn.WriteMessage(websocket.TextMessage, pingMsg)
			rc.connMu.Unlock()
			if err != nil {
				return
			}
		case <-rc.stopCh:
			return
		}
	}
}

func (rc *RelayClient) readPump(done func()) {
	defer done()

	rc.conn.SetReadLimit(1 << 20)
	rc.conn.SetPongHandler(func(string) error {
		rc.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
		return nil
	})

	for {
		rc.conn.SetReadDeadline(time.Now().Add(300 * time.Second))
		_, raw, err := rc.conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				debug.Log("tunnel", "relay-client: read error: %v", err)
			}
			return
		}

		var relayMsg struct {
			Type       string `json:"type"`
			SessionID  string `json:"session_id,omitempty"`
			EventID    string `json:"event_id,omitempty"`
			StreamID   string `json:"stream_id,omitempty"`
			Nonce      string `json:"nonce,omitempty"`
			Ciphertext string `json:"ciphertext,omitempty"`
			Role       string `json:"role,omitempty"`
		}
		if json.Unmarshal(raw, &relayMsg) != nil {
			continue
		}

		switch relayMsg.Type {
		case "connected":
			debug.Log("tunnel", "relay-client: confirmed as %s", relayMsg.Role)

		case "pong":
			// keepalive

		case "encrypted":
			plaintext, err := rc.crypto.Decrypt(relayMsg.Nonce, relayMsg.Ciphertext)
			if err != nil {
				debug.Log("tunnel", "relay-client: decrypt error: %v", err)
				continue
			}
			var msg GatewayMessage
			if json.Unmarshal(plaintext, &msg) != nil {
				continue
			}
			if msg.SessionID == "" {
				msg.SessionID = relayMsg.SessionID
			}
			if msg.EventID == "" {
				msg.EventID = relayMsg.EventID
			}
			if msg.StreamID == "" {
				msg.StreamID = relayMsg.StreamID
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
// Safe to call after Close — returns error instead of panicking.
func (rc *RelayClient) Send(msg GatewayMessage) error {
	rc.closeMu.Lock()
	if rc.closed {
		rc.closeMu.Unlock()
		return fmt.Errorf("relay client closed")
	}
	rc.closeMu.Unlock()

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
	envelope := struct {
		Type       string `json:"type"`
		SessionID  string `json:"session_id,omitempty"`
		EventID    string `json:"event_id,omitempty"`
		StreamID   string `json:"stream_id,omitempty"`
		Nonce      string `json:"nonce"`
		Ciphertext string `json:"ciphertext"`
	}{
		Type:       relayMsg["type"],
		SessionID:  msg.SessionID,
		EventID:    msg.EventID,
		StreamID:   msg.StreamID,
		Nonce:      relayMsg["nonce"],
		Ciphertext: relayMsg["ciphertext"],
	}
	data, err := json.Marshal(envelope)
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

func (rc *RelayClient) OnMessage(fn func(msg GatewayMessage)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onMessage = fn
}

func (rc *RelayClient) Close() {
	rc.closeMu.Lock()
	rc.closed = true
	rc.closeMu.Unlock()
	close(rc.stopCh)
}

func (rc *RelayClient) ConnectURL() string {
	return fmt.Sprintf("%s/ws?role=client&token=%s", rc.relayURL, rc.token)
}

func (rc *RelayClient) Token() string {
	return rc.token
}
