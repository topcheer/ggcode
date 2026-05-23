package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// DefaultRelayURL is the default ggcode-relay server URL.
const DefaultRelayURL = "wss://gateway.ggcode.dev"

// Session manages a relay session: connects to ggcode-relay, generates QR code.
//
// Usage:
//
//	sess := tunnel.NewSession("wss://relay.ggcode.app")
//	info, err := sess.Start(ctx)
//	fmt.Println(info.ConnectURL)
//	fmt.Println(info.QRCode)
//	sess.Send(msg)
//	sess.OnMessage(func(m) { ... })
//	sess.Stop()
type Session struct {
	relayURL string
	client   *RelayClient
	token    string
	onMsg    func(msg GatewayMessage)
	onConn   func(info RelayConnectedState)
	mu       sync.RWMutex
	info     *SessionInfo
}

// SessionInfo contains the connection details after a session starts.
type SessionInfo struct {
	ConnectURL string // wss://relay.ggcode.app/ws?role=client&token=abc123
	Token      string
	QRCode     string // terminal-friendly QR code (text)
	QRCodePNG  []byte // PNG image bytes for GUI display
	QRLines    []string
}

// NewSession creates a new relay session.
func NewSession(relayURL string) *Session {
	return &Session{relayURL: strings.TrimSuffix(relayURL, "/")}
}

// Start connects to the relay server and returns connection info.
func (s *Session) Start(ctx context.Context) (*SessionInfo, error) {
	// Generate random token (48 hex chars = 24 bytes → AES-192)
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	s.token = hex.EncodeToString(tokenBytes)

	// Create relay client
	client, err := NewRelayClient(s.relayURL, s.token)
	if err != nil {
		return nil, err
	}
	s.client = client

	// Wire handlers before connect so the initial relay callbacks cannot race
	// with handler registration.
	client.OnMessage(func(msg GatewayMessage) {
		s.mu.RLock()
		fn := s.onMsg
		s.mu.RUnlock()
		if fn != nil {
			fn(msg)
		}
	})
	client.OnConnected(func(info RelayConnectedState) {
		s.mu.RLock()
		fn := s.onConn
		s.mu.RUnlock()
		if fn != nil {
			fn(info)
		}
	})

	// Connect to relay
	if err := client.Connect(); err != nil {
		return nil, err
	}

	// Build connect URL
	connectURL := client.ConnectURL()

	// Generate QR code
	qrStr, _ := QRCodeForURL(connectURL)
	qrLines, _ := QRCodeLines(connectURL)
	qrPNG, _ := QRCodePNG(connectURL)

	info := &SessionInfo{
		ConnectURL: connectURL,
		Token:      s.token,
		QRCode:     qrStr,
		QRCodePNG:  qrPNG,
		QRLines:    qrLines,
	}

	s.mu.Lock()
	s.info = info
	s.mu.Unlock()

	return info, nil
}

func (s *Session) OnMessage(fn func(msg GatewayMessage)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onMsg = fn
}

func (s *Session) OnConnected(fn func(info RelayConnectedState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConn = fn
}

func (s *Session) Send(msg GatewayMessage) error {
	return s.client.Send(msg)
}

func (s *Session) SendActiveSession(sessionID string) error {
	return s.client.SendActiveSession(sessionID)
}

func (s *Session) Info() *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.info
}

func (s *Session) Stop() {
	if s.client != nil {
		s.client.Close()
	}
}

func (s *Session) StopGracefully(timeout time.Duration) {
	if s.client != nil {
		s.client.CloseGracefully(timeout)
	}
}

func (s *Session) DestroyGracefully(timeout time.Duration) {
	if s.client != nil {
		_ = s.client.DestroyRoom()
		s.client.CloseGracefully(timeout)
	}
}
