package tunnel

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Session manages a complete tunnel session: WebSocket gateway + localhost.run tunnel + QR code.
//
// All modes (TUI, daemon, desktop) use this same interface:
//
//	sess := tunnel.NewSession()
//	info, err := sess.Start(ctx)        // starts gateway + tunnel
//	fmt.Println(info.QRCode)            // print QR to terminal
//	fmt.Println(info.ConnectURL)        // wss://xxx.lhr.life/ws?token=abc
//	sess.Send(msg)                       // push to connected client
//	sess.OnMessage(func(m) { ... })      // receive from client
//	sess.Stop()
type Session struct {
	gateway *Gateway
	tunnel  *Tunnel
	onMsg   func(msg GatewayMessage)
	onConn  func() // called when a client connects

	mu   sync.RWMutex
	info *SessionInfo
}

// SessionInfo contains the connection details after a session starts.
type SessionInfo struct {
	ConnectURL string // wss://xxx.lhr.life/ws?token=abc123
	PublicURL  string // https://xxx.lhr.life
	Token      string // auth token
	Port       int    // local gateway port
	QRCode     string // terminal-friendly QR code (text)
	QRCodePNG  []byte // PNG image bytes for GUI display
	QRLines    []string
}

// NewSession creates a new tunnel session.
func NewSession() *Session {
	return &Session{
		gateway: NewGateway(),
	}
}

// Start launches the WebSocket gateway and establishes the tunnel.
// Blocks until the tunnel URL is received (typically 3-10 seconds).
func (s *Session) Start(ctx context.Context) (*SessionInfo, error) {
	// 1. Start WebSocket gateway
	port, token, err := s.gateway.Start()
	if err != nil {
		return nil, fmt.Errorf("start gateway: %w", err)
	}

	// 2. Start tunnel
	s.tunnel = New(port)
	publicURL, err := s.tunnel.Start(ctx)
	if err != nil {
		s.gateway.Close()
		return nil, fmt.Errorf("start tunnel: %w", err)
	}

	// 3. Build connect URL (strip https://, use wss://)
	host := strings.TrimPrefix(publicURL, "https://")
	connectURL := s.gateway.ConnectURL(host)

	// 4. Generate QR code
	qrStr, _ := QRCodeForURL(connectURL)
	qrLines, _ := QRCodeLines(connectURL)
	qrPNG, _ := QRCodePNG(connectURL)

	info := &SessionInfo{
		ConnectURL: connectURL,
		PublicURL:  publicURL,
		Token:      token,
		Port:       port,
		QRCode:     qrStr,
		QRCodePNG:  qrPNG,
		QRLines:    qrLines,
	}

	s.mu.Lock()
	s.info = info
	s.mu.Unlock()

	// Wire up message handler
	s.gateway.OnMessage(func(msg GatewayMessage) {
		if s.onMsg != nil {
			s.onMsg(msg)
		}
	})

	// Wire up connect handler
	s.gateway.OnConnect(func() {
		if s.onConn != nil {
			s.onConn()
		}
	})

	return info, nil
}

// OnMessage sets the handler for incoming messages from connected clients.
func (s *Session) OnMessage(fn func(msg GatewayMessage)) {
	s.onMsg = fn
}

func (s *Session) OnConnect(fn func()) {
	s.onConn = fn
}

// Send pushes a message to the connected client.
func (s *Session) Send(msg GatewayMessage) error {
	return s.gateway.Send(msg)
}

// Info returns the current session info, or nil if not started.
func (s *Session) Info() *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.info
}

// Stop closes the tunnel and gateway.
func (s *Session) Stop() {
	if s.tunnel != nil {
		s.tunnel.Stop()
	}
	if s.gateway != nil {
		s.gateway.Close()
	}
}
