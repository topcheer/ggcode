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

	conn           *websocket.Conn
	connMu         sync.Mutex
	sendCh         chan []byte
	closed         bool
	closeMu        sync.Mutex
	stopCh         chan struct{}
	gracefulStopCh chan struct{}
	runDone        chan struct{}
	stopOnce       sync.Once
	gracefulOnce   sync.Once

	onMessage   func(msg GatewayMessage)
	onConnected func(info RelayConnectedState)
	mu          sync.RWMutex
}

type RelayConnectedState struct {
	Role         string
	SessionID    string
	HistoryCount int
}

const (
	relayPingInterval = 20 * time.Second
	relayReadTimeout  = 75 * time.Second
)

func NewRelayClient(relayURL, token string) (*RelayClient, error) {
	crypto, err := NewCrypto(token)
	if err != nil {
		return nil, err
	}
	return &RelayClient{
		relayURL:       strings.TrimSuffix(relayURL, "/"),
		token:          token,
		crypto:         crypto,
		sendCh:         make(chan []byte, 256),
		stopCh:         make(chan struct{}),
		gracefulStopCh: make(chan struct{}),
		runDone:        make(chan struct{}),
	}, nil
}

// Connect starts the connection loop. It connects, runs pumps, and auto-reconnects.
func (rc *RelayClient) Connect() error {
	conn, err := rc.dial()
	if err != nil {
		return err
	}
	go rc.run(conn)
	return nil
}

func (rc *RelayClient) dial() (*websocket.Conn, error) {
	url := fmt.Sprintf("%s/ws?role=server&token=%s", rc.relayURL, rc.token)
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("relay dial: %w", err)
	}
	rc.connMu.Lock()
	rc.conn = conn
	rc.connMu.Unlock()
	return conn, nil
}

func (rc *RelayClient) clearConn(conn *websocket.Conn) {
	rc.connMu.Lock()
	defer rc.connMu.Unlock()
	if rc.conn == conn {
		rc.conn = nil
	}
}

func (rc *RelayClient) currentConn() *websocket.Conn {
	rc.connMu.Lock()
	defer rc.connMu.Unlock()
	return rc.conn
}

func (rc *RelayClient) run(conn *websocket.Conn) {
	defer close(rc.runDone)
	for {
		done := make(chan struct{})
		var once sync.Once
		closeDone := func() { once.Do(func() { close(done) }) }
		var wg sync.WaitGroup
		wg.Add(2)

		go func(activeConn *websocket.Conn) {
			defer wg.Done()
			rc.writePump(activeConn, closeDone)
		}(conn)
		go func(activeConn *websocket.Conn) {
			defer wg.Done()
			rc.readPump(activeConn, closeDone)
		}(conn)

		<-done // Wait for either pump to exit
		_ = conn.Close()
		wg.Wait()
		rc.clearConn(conn)

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

			nextConn, err := rc.dial()
			if err != nil {
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
			conn = nextConn
			break
		}
	}
}

func (rc *RelayClient) writePump(conn *websocket.Conn, done func()) {
	defer done()
	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
	ticker := time.NewTicker(relayPingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-rc.sendCh:
			if !ok {
				return
			}
			err := conn.WriteMessage(websocket.TextMessage, msg)
			if err != nil {
				return
			}
		case <-ticker.C:
			err := conn.WriteMessage(websocket.TextMessage, pingMsg)
			if err != nil {
				return
			}
		case <-rc.gracefulStopCh:
			for {
				select {
				case msg, ok := <-rc.sendCh:
					if !ok {
						return
					}
					err := conn.WriteMessage(websocket.TextMessage, msg)
					if err != nil {
						return
					}
				default:
					_ = conn.WriteControl(
						websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
						time.Now().Add(time.Second),
					)
					return
				}
			}
		case <-rc.stopCh:
			return
		}
	}
}

func (rc *RelayClient) readPump(conn *websocket.Conn, done func()) {
	defer done()

	conn.SetReadLimit(1 << 20)
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(relayReadTimeout))
		return nil
	})

	for {
		conn.SetReadDeadline(time.Now().Add(relayReadTimeout))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if err != io.EOF {
				debug.Log("tunnel", "relay-client: read error: %v", err)
			}
			return
		}

		var relayMsg struct {
			Type       string          `json:"type"`
			SessionID  string          `json:"session_id,omitempty"`
			EventID    string          `json:"event_id,omitempty"`
			StreamID   string          `json:"stream_id,omitempty"`
			Count      int             `json:"count,omitempty"`
			Nonce      string          `json:"nonce,omitempty"`
			Ciphertext string          `json:"ciphertext,omitempty"`
			Role       string          `json:"role,omitempty"`
			Data       json.RawMessage `json:"data,omitempty"`
		}
		if json.Unmarshal(raw, &relayMsg) != nil {
			continue
		}

		switch relayMsg.Type {
		case "connected":
			debug.Log("tunnel", "relay-client: confirmed as %s", relayMsg.Role)
			rc.mu.RLock()
			fn := rc.onConnected
			rc.mu.RUnlock()
			if fn != nil {
				fn(RelayConnectedState{
					Role:         relayMsg.Role,
					SessionID:    relayMsg.SessionID,
					HistoryCount: relayMsg.Count,
				})
			}

		case EventActiveSession:
			rc.deliver(GatewayMessage{
				Type:      EventActiveSession,
				SessionID: relayMsg.SessionID,
				Data:      relayMsg.Data,
			})

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
			rc.deliver(msg)

		case "language_change":
			// Forward to desktop as a plaintext command
			rc.deliver(GatewayMessage{
				Type:      CmdLanguageChange,
				EventID:   relayMsg.EventID,
				SessionID: relayMsg.SessionID,
				Data:      relayMsg.Data,
			})
		case "theme_change":
			rc.deliver(GatewayMessage{
				Type:      CmdThemeChange,
				EventID:   relayMsg.EventID,
				SessionID: relayMsg.SessionID,
				Data:      relayMsg.Data,
			})
		}
	}
}

func (rc *RelayClient) SendActiveSession(sessionID string) error {
	rc.closeMu.Lock()
	if rc.closed {
		rc.closeMu.Unlock()
		return fmt.Errorf("relay client closed")
	}
	rc.closeMu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	data, err := json.Marshal(struct {
		Type      string          `json:"type"`
		SessionID string          `json:"session_id,omitempty"`
		Data      json.RawMessage `json:"data,omitempty"`
	}{
		Type:      EventActiveSession,
		SessionID: sessionID,
		Data:      mustRawJSON(ActiveSessionData{SessionID: sessionID}),
	})
	if err != nil {
		return err
	}
	return rc.enqueueRaw(data)
}

func mustRawJSON(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// deliver calls the onMessage callback safely.
func (rc *RelayClient) deliver(msg GatewayMessage) {
	rc.mu.RLock()
	fn := rc.onMessage
	rc.mu.RUnlock()
	if fn != nil {
		fn(msg)
	}
}

// Send encrypts and enqueues a GatewayMessage for delivery to the relay.
// It applies backpressure instead of dropping when the relay is reconnecting
// or the write pump is briefly saturated.
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

	return rc.enqueueRaw(data)
}

func (rc *RelayClient) DestroyRoom() error {
	data, err := json.Marshal(struct {
		Type string `json:"type"`
	}{
		Type: "destroy_room",
	})
	if err != nil {
		return err
	}
	return rc.enqueueRaw(data)
}

func (rc *RelayClient) enqueueRaw(data []byte) error {
	rc.closeMu.Lock()
	if rc.closed {
		rc.closeMu.Unlock()
		return fmt.Errorf("relay client closed")
	}
	rc.closeMu.Unlock()
	select {
	case rc.sendCh <- data:
		return nil
	case <-rc.stopCh:
		return fmt.Errorf("relay client closed")
	}
}

func (rc *RelayClient) OnMessage(fn func(msg GatewayMessage)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onMessage = fn
}

func (rc *RelayClient) OnConnected(fn func(info RelayConnectedState)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onConnected = fn
}

func (rc *RelayClient) Close() {
	rc.closeMu.Lock()
	rc.closed = true
	rc.closeMu.Unlock()
	rc.stopOnce.Do(func() {
		close(rc.stopCh)
		if conn := rc.currentConn(); conn != nil {
			_ = conn.Close()
		}
	})
}

func (rc *RelayClient) CloseGracefully(timeout time.Duration) {
	rc.closeMu.Lock()
	rc.closed = true
	rc.closeMu.Unlock()

	if rc.currentConn() == nil {
		rc.Close()
		return
	}

	rc.gracefulOnce.Do(func() {
		close(rc.gracefulStopCh)
	})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-rc.runDone:
	case <-timer.C:
		rc.Close()
	}
}

func (rc *RelayClient) ConnectURL() string {
	return fmt.Sprintf("%s/ws?role=client&token=%s", rc.relayURL, rc.token)
}

func (rc *RelayClient) Token() string {
	return rc.token
}
