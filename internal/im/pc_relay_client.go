package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	pcHeartbeatInterval     = 15 * time.Second
	pcHeartbeatTimeout      = 45 * time.Second
	pcRequestTimeout        = 30 * time.Second
	pcReadyTimeout          = 10 * time.Second
	pcInitialReconnectDelay = 1 * time.Second
	pcMaxReconnectDelay     = 30 * time.Second
)

// pcRelayClient manages the WebSocket connection between Provider and Relay Server.
type pcRelayClient struct {
	wsURL      string
	providerID string

	// Callbacks
	onFrame         func(sessionID string, envelope *pcEncryptedEnvelope)
	onSessionClosed func(sessionID, reason string)
	onError         func(message string)
	onReady         func()

	mu       sync.Mutex
	conn     *websocket.Conn
	disposed bool

	writeMu sync.Mutex

	// Ready signaling
	readyCh chan struct{}

	// Request-response matching
	pendingCreates  map[string]chan *pcRelaySessionCreated
	pendingRenewals map[string]chan *pcRelaySessionRenewed
}

func newPCRelayClient(wsURL, providerID string) *pcRelayClient {
	return &pcRelayClient{
		wsURL:           wsURL,
		providerID:      providerID,
		pendingCreates:  make(map[string]chan *pcRelaySessionCreated),
		pendingRenewals: make(map[string]chan *pcRelaySessionRenewed),
	}
}

// buildProviderURL appends providerId as a query parameter.
func (c *pcRelayClient) buildProviderURL() string {
	if c.providerID == "" {
		return c.wsURL
	}
	u, err := url.Parse(c.wsURL)
	if err != nil {
		return c.wsURL + "?providerId=" + url.QueryEscape(c.providerID)
	}
	q := u.Query()
	q.Set("providerId", c.providerID)
	u.RawQuery = q.Encode()
	return u.String()
}

// Connect establishes the WebSocket connection and waits for relay:provider_ready.
func (c *pcRelayClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.disposed {
		c.mu.Unlock()
		return fmt.Errorf("relay client is disposed")
	}
	c.mu.Unlock()

	wsURL := c.buildProviderURL()
	debug.Log("pc", "relay connecting to %s", wsURL)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("connect relay: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.readyCh = make(chan struct{})
	c.mu.Unlock()

	// Start heartbeat
	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	go c.HeartbeatLoop(heartbeatCtx)

	// Start ReadLoop in background — it will signal readyCh on provider_ready
	safego.Go("im.pcRelay.readLoop", func() {
		c.ReadLoop(ctx)
		heartbeatCancel()
		// Clean up conn on exit
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	})

	// Wait for provider_ready
	debug.Log("pc", "relay websocket connected, waiting for provider_ready")
	select {
	case <-c.readyCh:
		debug.Log("pc", "relay: provider_ready confirmed")
		return nil
	case <-time.After(pcReadyTimeout):
		conn.Close()
		return fmt.Errorf("timed out waiting for relay:provider_ready after %s", pcReadyTimeout)
	case <-ctx.Done():
		conn.Close()
		return ctx.Err()
	}
}

// Dispose closes the connection and prevents reconnects.
func (c *pcRelayClient) Dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disposed = true
	if c.conn != nil {
		_ = c.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "provider shutdown"),
			time.Now().Add(5*time.Second),
		)
		_ = c.conn.Close()
		c.conn = nil
	}
	// Signal all pending requests
	for _, ch := range c.pendingCreates {
		close(ch)
	}
	for _, ch := range c.pendingRenewals {
		close(ch)
	}
	c.pendingCreates = make(map[string]chan *pcRelaySessionCreated)
	c.pendingRenewals = make(map[string]chan *pcRelaySessionRenewed)
}

// ReadLoop reads messages from the WebSocket and dispatches them.
// It blocks until the connection is closed or an error occurs.
func (c *pcRelayClient) ReadLoop(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, data, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read relay: %w", err)
		}
		if err := c.handleMessage(data); err != nil {
			debug.Log("pc", "relay message handling error: %v", err)
		}
	}
}

// HeartbeatLoop sends ping frames at regular intervals.
func (c *pcRelayClient) HeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(pcHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()
			if conn == nil {
				continue
			}
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				debug.Log("pc", "heartbeat ping failed: %v", err)
				return
			}
		}
	}
}

// CreateSession sends a provider:create_session request and waits for the response.
func (c *pcRelayClient) CreateSession(ctx context.Context, ttlMS *int, label string, groupMode *bool) (*pcRelaySessionCreated, error) {
	requestID := pcGenerateRequestID()
	msg := pcProviderCreateSession{
		Type:      pcTypeCreateSession,
		RequestID: requestID,
		TTLMS:     ttlMS,
		Label:     label,
		GroupMode: groupMode,
	}

	ch := make(chan *pcRelaySessionCreated, 1)
	c.mu.Lock()
	c.pendingCreates[requestID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pendingCreates, requestID)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(msg); err != nil {
		return nil, fmt.Errorf("send create_session: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("relay client disposed during create_session")
		}
		return resp, nil
	case <-time.After(pcRequestTimeout):
		return nil, fmt.Errorf("create_session timed out after %s", pcRequestTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RenewSession sends a provider:renew_session request and waits for the response.
func (c *pcRelayClient) RenewSession(ctx context.Context, sessionID string, ttlMS int) (*pcRelaySessionRenewed, error) {
	requestID := pcGenerateRequestID()
	msg := pcProviderRenewSession{
		Type:      pcTypeRenewSession,
		RequestID: requestID,
		SessionID: sessionID,
		TTLMS:     ttlMS,
	}

	ch := make(chan *pcRelaySessionRenewed, 1)
	c.mu.Lock()
	c.pendingRenewals[requestID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pendingRenewals, requestID)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(msg); err != nil {
		return nil, fmt.Errorf("send renew_session: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("relay client disposed during renew_session")
		}
		return resp, nil
	case <-time.After(pcRequestTimeout):
		return nil, fmt.Errorf("renew_session timed out after %s", pcRequestTimeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SendFrame sends an encrypted frame to the relay.
func (c *pcRelayClient) SendFrame(sessionID string, envelope *pcEncryptedEnvelope, targetAppID string) error {
	msg := pcProviderFrame{
		Type:        pcTypeFrame,
		SessionID:   sessionID,
		Envelope:    *envelope,
		TargetAppID: targetAppID,
	}
	return c.writeJSON(msg)
}

// CloseSession sends a provider:close_session message.
func (c *pcRelayClient) CloseSession(sessionID, reason string) error {
	msg := pcProviderCloseSession{
		Type:      pcTypeCloseSession,
		SessionID: sessionID,
		Reason:    reason,
	}
	return c.writeJSON(msg)
}

// CloseApp sends a provider:close_app message to kick a participant.
func (c *pcRelayClient) CloseApp(sessionID, appID, reason string) error {
	msg := pcProviderCloseApp{
		Type:      pcTypeCloseApp,
		SessionID: sessionID,
		AppID:     appID,
		Reason:    reason,
	}
	return c.writeJSON(msg)
}

func (c *pcRelayClient) handleMessage(data []byte) error {
	// Peek at the type field
	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return fmt.Errorf("parse relay message type: %w", err)
	}

	debug.Log("pc", "relay message type=%s len=%d", peek.Type, len(data))

	switch peek.Type {
	case pcTypeProviderReady:
		debug.Log("pc", "relay: provider_ready received")
		// Signal ready
		c.mu.Lock()
		if c.readyCh != nil {
			close(c.readyCh)
			c.readyCh = nil
		}
		c.mu.Unlock()
		if c.onReady != nil {
			c.onReady()
		}

	case pcTypeSessionCreated:
		var msg pcRelaySessionCreated
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("parse session_created: %w", err)
		}
		c.mu.Lock()
		ch, ok := c.pendingCreates[msg.RequestID]
		if ok {
			ch <- &msg
			delete(c.pendingCreates, msg.RequestID)
		}
		c.mu.Unlock()

	case pcTypeSessionRenewed:
		var msg pcRelaySessionRenewed
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("parse session_renewed: %w", err)
		}
		c.mu.Lock()
		ch, ok := c.pendingRenewals[msg.RequestID]
		if ok {
			ch <- &msg
			delete(c.pendingRenewals, msg.RequestID)
		}
		c.mu.Unlock()

	case pcTypeRelayFrame:
		var msg pcRelayFrame
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("parse relay:frame: %w", err)
		}
		if c.onFrame != nil {
			c.onFrame(msg.SessionID, &msg.Envelope)
		}

	case pcTypeSessionClosed:
		var msg pcRelaySessionClosed
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("parse session_closed: %w", err)
		}
		if c.onSessionClosed != nil {
			c.onSessionClosed(msg.SessionID, msg.Reason)
		}

	case pcTypeError:
		var msg pcRelayError
		if err := json.Unmarshal(data, &msg); err != nil {
			return fmt.Errorf("parse relay:error: %w", err)
		}
		debug.Log("pc", "relay error code=%s message=%s session=%s request=%s",
			msg.Code, msg.Message, msg.SessionID, msg.RequestID)
		// Forward to pending requests if applicable
		c.mu.Lock()
		if msg.RequestID != "" {
			if ch, ok := c.pendingCreates[msg.RequestID]; ok {
				close(ch)
				delete(c.pendingCreates, msg.RequestID)
			}
			if ch, ok := c.pendingRenewals[msg.RequestID]; ok {
				close(ch)
				delete(c.pendingRenewals, msg.RequestID)
			}
		}
		c.mu.Unlock()
		if c.onError != nil {
			c.onError(msg.Message)
		}

	default:
		debug.Log("pc", "unknown relay message type: %s", peek.Type)
	}

	return nil
}

func (c *pcRelayClient) writeJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected to relay")
	}
	return conn.WriteJSON(v)
}
