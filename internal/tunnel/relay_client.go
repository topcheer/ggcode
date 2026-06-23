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
	"github.com/topcheer/ggcode/internal/safego"
)

// RelayClient connects to the ggcode-relay server as the "server" role.
// It auto-reconnects on disconnect with exponential backoff.
type RelayClient struct {
	relayURL string
	token    string
	crypto   *Crypto
	role     string
	meta     RelayClientMetadata
	desc     ShareDescriptor

	conn           *websocket.Conn
	connMu         sync.Mutex
	sendCh         chan []byte
	pendingMu      sync.Mutex
	pendingFront   [][]byte
	closed         bool
	closeMu        sync.Mutex
	stopCh         chan struct{}
	gracefulStopCh chan struct{}
	runDone        chan struct{}
	stopOnce       sync.Once
	gracefulOnce   sync.Once

	onMessage   func(msg GatewayMessage)
	onConnected func(info RelayConnectedState)
	onAck       func(ackType, messageID string)
	mu          sync.RWMutex
}

type relayKeyOffer struct {
	ClientPublicKey string `json:"client_public_key"`
}

type relayKeyAccept struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type RelayConnectedState struct {
	Role            string
	SessionID       string
	Generation      uint64
	AuthorityEpoch  uint64
	HistoryCount    int
	LastEventID     string
	ProjectionHash  string
	ProtocolVersion int
	ResumeComplete  bool
	ShareMode       string
	RoomID          string
	ConnectMode     string
	Notice          string
	AuthExpiresAt   time.Time
	RenewExpiresAt  time.Time
}

const (
	relayPingInterval = 20 * time.Second
	relayReadTimeout  = 75 * time.Second
	relayWriteTimeout = 30 * time.Second
)

func relayReconnectDelay(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 5 * time.Second
	case attempt == 2:
		return 10 * time.Second
	case attempt == 3:
		return 20 * time.Second
	case attempt <= 6:
		return 40 * time.Second
	case attempt <= 12:
		return 2 * time.Minute
	default:
		// Persistent failure (relay down >24 min): keep trying every 5 min.
		// Messages are buffered in the broker outbound queue and replayed
		// once the connection is restored.
		return 5 * time.Minute
	}
}

func NewRelayClient(_, _ string) (*RelayClient, error) {
	return nil, fmt.Errorf("legacy relay clients are unsupported; use an issued share v3 descriptor")
}

func NewRelayClientWithDescriptor(relayURL string, desc ShareDescriptor, role string, meta RelayClientMetadata) (*RelayClient, error) {
	if err := validateRelayURLSecurity(relayURL); err != nil {
		return nil, err
	}
	if !desc.IsV3() {
		return nil, fmt.Errorf("share v3 descriptor required")
	}
	crypto, err := NewCrypto(desc.CryptoKey)
	if err != nil {
		return nil, err
	}
	if meta.Capabilities == nil {
		meta.Capabilities = append([]string(nil), defaultShareCapabilities...)
	}
	if role == "" {
		role = "server"
	}
	return &RelayClient{
		relayURL:       strings.TrimSuffix(relayURL, "/"),
		token:          desc.RoomID,
		crypto:         crypto,
		role:           role,
		meta:           meta,
		desc:           desc,
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
	safego.Go("tunnel.relayClient.run", func() { rc.run(conn) })
	return nil
}

func (rc *RelayClient) dial() (*websocket.Conn, error) {
	url := rc.currentShareDescriptor().RuntimeConnectURL(rc.relayURL, rc.role, rc.meta, true)
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{})
	if err != nil {
		if resp != nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			reason := strings.TrimSpace(string(body))
			if readErr == nil && reason != "" {
				return nil, fmt.Errorf("relay dial: %s (%s)", reason, resp.Status)
			}
			return nil, fmt.Errorf("relay dial: %s", resp.Status)
		}
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

		curConn := conn
		safego.Go("tunnel.relayClient.writePump", func() {
			defer wg.Done()
			rc.writePump(curConn, closeDone)
		})
		safego.Go("tunnel.relayClient.readPump", func() {
			defer wg.Done()
			rc.readPump(curConn, closeDone)
		})

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
				backoff := relayReconnectDelay(attempt + 1)
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

	// writeMsg writes a message with a write deadline. On failure it pushes
	// the message back to pendingFront and returns false.
	writeMsg := func(msg []byte) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			conn.SetWriteDeadline(time.Time{}) // clear deadline
			rc.pushPendingFront(msg)
			return false
		}
		return true
	}

	for {
		if msg, ok := rc.popPendingFront(); ok {
			if !writeMsg(msg) {
				return
			}
			continue
		}
		select {
		case msg, ok := <-rc.sendCh:
			if !ok {
				return
			}
			if !writeMsg(msg) {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, pingMsg); err != nil {
				conn.SetWriteDeadline(time.Time{})
				return
			}
		case <-rc.gracefulStopCh:
			for {
				select {
				case msg, ok := <-rc.sendCh:
					if !ok {
						return
					}
					if !writeMsg(msg) {
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

func (rc *RelayClient) pushPendingFront(msg []byte) {
	if len(msg) == 0 {
		return
	}
	rc.pendingMu.Lock()
	defer rc.pendingMu.Unlock()
	rc.pendingFront = append([][]byte{append([]byte(nil), msg...)}, rc.pendingFront...)
}

func (rc *RelayClient) popPendingFront() ([]byte, bool) {
	rc.pendingMu.Lock()
	defer rc.pendingMu.Unlock()
	if len(rc.pendingFront) == 0 {
		return nil, false
	}
	msg := rc.pendingFront[0]
	rc.pendingFront = rc.pendingFront[1:]
	return msg, true
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
			Type           string          `json:"type"`
			SessionID      string          `json:"session_id,omitempty"`
			EventID        string          `json:"event_id,omitempty"`
			StreamID       string          `json:"stream_id,omitempty"`
			ClientID       string          `json:"client_id,omitempty"`
			MessageID      string          `json:"message_id,omitempty"`
			Generation     uint64          `json:"generation,omitempty"`
			AuthorityEpoch uint64          `json:"authority_epoch,omitempty"`
			LastEventID    string          `json:"last_event_id,omitempty"`
			ProjectionHash string          `json:"projection_hash,omitempty"`
			Count          int             `json:"count,omitempty"`
			Nonce          string          `json:"nonce,omitempty"`
			Ciphertext     string          `json:"ciphertext,omitempty"`
			Role           string          `json:"role,omitempty"`
			Data           json.RawMessage `json:"data,omitempty"`
		}
		if json.Unmarshal(raw, &relayMsg) != nil {
			continue
		}

		switch relayMsg.Type {
		case "connected":
			debug.Log("tunnel", "relay-client: confirmed as %s", relayMsg.Role)
			state := RelayConnectedState{
				Role:           relayMsg.Role,
				SessionID:      relayMsg.SessionID,
				Generation:     relayMsg.Generation,
				AuthorityEpoch: relayMsg.AuthorityEpoch,
				HistoryCount:   relayMsg.Count,
				LastEventID:    relayMsg.LastEventID,
				ProjectionHash: relayMsg.ProjectionHash,
			}
			if state.Role == "" {
				state.Role = rc.role
			}
			if len(relayMsg.Data) > 0 {
				var meta struct {
					ProtocolVersion int    `json:"protocol_version,omitempty"`
					ResumeComplete  bool   `json:"resume_complete,omitempty"`
					ShareMode       string `json:"share_mode,omitempty"`
					RoomID          string `json:"room_id,omitempty"`
					ConnectMode     string `json:"connect_mode,omitempty"`
					Notice          string `json:"notice,omitempty"`
					RenewToken      string `json:"renew_token,omitempty"`
					AuthExpiresAt   string `json:"auth_expires_at,omitempty"`
					RenewExpiresAt  string `json:"renew_expires_at,omitempty"`
				}
				if err := json.Unmarshal(relayMsg.Data, &meta); err == nil {
					state.ProtocolVersion = meta.ProtocolVersion
					state.ResumeComplete = meta.ResumeComplete
					state.ShareMode = meta.ShareMode
					state.RoomID = meta.RoomID
					state.ConnectMode = meta.ConnectMode
					state.Notice = meta.Notice
					if meta.AuthExpiresAt != "" {
						if ts, err := time.Parse(time.RFC3339, meta.AuthExpiresAt); err == nil {
							state.AuthExpiresAt = ts
						}
					}
					if meta.RenewExpiresAt != "" {
						if ts, err := time.Parse(time.RFC3339, meta.RenewExpiresAt); err == nil {
							state.RenewExpiresAt = ts
						}
					}
					if meta.RenewToken != "" {
						rc.updateShareDescriptor(func(desc *ShareDescriptor) {
							desc.RenewToken = meta.RenewToken
							if !state.RenewExpiresAt.IsZero() {
								desc.RenewExpiresAt = state.RenewExpiresAt
							}
							if !state.AuthExpiresAt.IsZero() {
								desc.AuthExpiresAt = state.AuthExpiresAt
							}
						})
					}
				}
			}
			rc.mu.RLock()
			fn := rc.onConnected
			rc.mu.RUnlock()
			if fn != nil {
				fn(state)
			}

		case EventActiveSession:
			rc.deliver(GatewayMessage{
				Type:      EventActiveSession,
				SessionID: relayMsg.SessionID,
				Data:      relayMsg.Data,
			})

		case "server_offline", "sharing_stopped":
			rc.deliver(GatewayMessage{
				Type:      relayMsg.Type,
				SessionID: relayMsg.SessionID,
				Data:      relayMsg.Data,
			})

		case "relay_ack":
			rc.mu.RLock()
			fn := rc.onAck
			rc.mu.RUnlock()
			if fn != nil && relayMsg.MessageID != "" {
				fn("relay_ack", relayMsg.MessageID)
			}

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
			if msg.AuthorityEpoch == 0 {
				msg.AuthorityEpoch = relayMsg.AuthorityEpoch
			}

			// Handle server_ack specially — deliver to ack callback, not general message handler.
			if msg.Type == EventServerAck {
				rc.mu.RLock()
				fn := rc.onAck
				rc.mu.RUnlock()
				if fn != nil {
					var ackData AckData
					if json.Unmarshal(msg.Data, &ackData) == nil && ackData.MessageID != "" {
						fn("server_ack", ackData.MessageID)
					}
				}
				continue
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
		case "key_offer":
			if rc.role == "server" {
				if err := rc.handleKeyOffer(relayMsg.ClientID, relayMsg.Data); err != nil {
					debug.Log("tunnel", "relay-client: key offer error: %v", err)
				}
			}
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

func (rc *RelayClient) SendActiveSession(sessionID string, authorityEpoch uint64, barrierEventID string, barrierOrdinal int64, projectionHash string) error {
	return rc.SendActiveSessionWithMode(sessionID, "", authorityEpoch, barrierEventID, barrierOrdinal, projectionHash, "", "", "")
}

func (rc *RelayClient) SendServerReady(authorityEpoch uint64) error {
	rc.closeMu.Lock()
	if rc.closed {
		rc.closeMu.Unlock()
		return fmt.Errorf("relay client closed")
	}
	rc.closeMu.Unlock()
	data, err := json.Marshal(struct {
		Type           string `json:"type"`
		AuthorityEpoch uint64 `json:"authority_epoch,omitempty"`
	}{
		Type:           EventServerReady,
		AuthorityEpoch: authorityEpoch,
	})
	if err != nil {
		return err
	}
	return rc.enqueueRaw(data)
}

func (rc *RelayClient) SendActiveSessionWithMode(sessionID, mode string, authorityEpoch uint64, barrierEventID string, barrierOrdinal int64, projectionHash string, workspacePath string, providerName string, modelName string) error {
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
		Type           string          `json:"type"`
		SessionID      string          `json:"session_id,omitempty"`
		AuthorityEpoch uint64          `json:"authority_epoch,omitempty"`
		ResumeMode     string          `json:"resume_mode,omitempty"`
		BarrierEventID string          `json:"barrier_event_id,omitempty"`
		BarrierOrdinal int64           `json:"barrier_ordinal,omitempty"`
		ProjectionHash string          `json:"projection_hash,omitempty"`
		Data           json.RawMessage `json:"data,omitempty"`
	}{
		Type:           EventActiveSession,
		SessionID:      sessionID,
		AuthorityEpoch: authorityEpoch,
		ResumeMode:     mode,
		BarrierEventID: barrierEventID,
		BarrierOrdinal: barrierOrdinal,
		ProjectionHash: projectionHash,
		Data: mustRawJSON(ActiveSessionData{
			SessionID:      sessionID,
			BarrierEventID: barrierEventID,
			BarrierOrdinal: barrierOrdinal,
			ProjectionHash: projectionHash,
			WorkspacePath:  workspacePath,
			ProviderName:   providerName,
			ModelName:      modelName,
		}),
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
	if msg.MessageID != "" {
		relayMsg["message_id"] = msg.MessageID
	}
	envelope := struct {
		Type           string `json:"type"`
		SessionID      string `json:"session_id,omitempty"`
		EventID        string `json:"event_id,omitempty"`
		StreamID       string `json:"stream_id,omitempty"`
		MessageID      string `json:"message_id,omitempty"`
		AuthorityEpoch uint64 `json:"authority_epoch,omitempty"`
		EventHash      string `json:"event_hash,omitempty"`
		Nonce          string `json:"nonce"`
		Ciphertext     string `json:"ciphertext"`
	}{
		Type:           relayMsg["type"],
		SessionID:      msg.SessionID,
		EventID:        msg.EventID,
		StreamID:       msg.StreamID,
		MessageID:      msg.MessageID,
		AuthorityEpoch: msg.AuthorityEpoch,
		EventHash:      ProjectionEventHash(msg),
		Nonce:          relayMsg["nonce"],
		Ciphertext:     relayMsg["ciphertext"],
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
		Type: "stop_sharing",
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

func (rc *RelayClient) OnAck(fn func(ackType, messageID string)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.onAck = fn
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
	return rc.currentShareDescriptor().PublicConnectURL(rc.relayURL)
}

func (rc *RelayClient) Token() string {
	return rc.token
}

func (rc *RelayClient) currentShareDescriptor() ShareDescriptor {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.desc
}

func (rc *RelayClient) updateShareDescriptor(fn func(desc *ShareDescriptor)) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	fn(&rc.desc)
	rc.token = rc.desc.RoomID
}

func (rc *RelayClient) handleKeyOffer(clientID string, raw json.RawMessage) error {
	if strings.TrimSpace(clientID) == "" {
		return fmt.Errorf("missing client id")
	}
	desc := rc.currentShareDescriptor()
	if !desc.IsV3() {
		return nil
	}
	if strings.TrimSpace(desc.RoomID) == "" {
		return fmt.Errorf("missing room id")
	}
	if strings.TrimSpace(desc.CryptoKey) == "" {
		return fmt.Errorf("missing room key")
	}
	if strings.TrimSpace(desc.ServerPrivateKey) == "" {
		return fmt.Errorf("missing server private key")
	}
	var offer relayKeyOffer
	if err := json.Unmarshal(raw, &offer); err != nil {
		return err
	}
	if strings.TrimSpace(offer.ClientPublicKey) == "" {
		return fmt.Errorf("missing client public key")
	}
	nonce, ciphertext, err := wrapShareRoomKey(desc.CryptoKey, desc.RoomID, clientID, desc.ServerPrivateKey, offer.ClientPublicKey)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Type     string         `json:"type"`
		ClientID string         `json:"client_id"`
		Data     relayKeyAccept `json:"data"`
	}{
		Type:     "key_accept",
		ClientID: clientID,
		Data: relayKeyAccept{
			Nonce:      nonce,
			Ciphertext: ciphertext,
		},
	})
	if err != nil {
		return err
	}
	return rc.enqueueRaw(payload)
}
