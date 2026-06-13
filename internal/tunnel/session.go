package tunnel

import (
	"context"
	"fmt"
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
	meta     RelayClientMetadata
	mu       sync.RWMutex
	info     *SessionInfo

	// cachedConnState holds the connected state received from the relay
	// before OnConnected was wired up (e.g. during sess.Start() before
	// NewBroker is created). When OnConnected is eventually called, we
	// replay this cached state so handleRelayConnected always runs.
	cachedConnState *RelayConnectedState
}

// SessionInfo contains the connection details after a session starts.
type SessionInfo struct {
	ConnectURL          string // ws/wss tunnel URL for the mobile client
	Token               string
	QRCode              string // terminal-friendly QR code (text)
	QRCodePNG           []byte // PNG image bytes for GUI display
	QRLines             []string
	ProtocolVersion     int
	ShareMode           string
	CompatibilityNotice string
	RoomID              string
	AuthExpiresAt       time.Time
	RenewExpiresAt      time.Time
}

type SessionOption func(*Session)

func WithClientMetadata(kind, version string) SessionOption {
	return func(s *Session) {
		s.meta = defaultRelayClientMetadata(kind, version)
	}
}

func WithClientCapabilities(capabilities ...string) SessionOption {
	return func(s *Session) {
		if s.meta.Capabilities == nil {
			s.meta = defaultRelayClientMetadata("", "")
		}
		s.meta.Capabilities = append([]string(nil), capabilities...)
	}
}

// NewSession creates a new relay session.
func NewSession(relayURL string, opts ...SessionOption) *Session {
	sess := &Session{
		relayURL: strings.TrimSuffix(relayURL, "/"),
		meta:     defaultRelayClientMetadata("", ""),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(sess)
		}
	}
	return sess
}

// Start connects to the relay server and returns connection info.
func (s *Session) Start(ctx context.Context) (*SessionInfo, error) {
	if err := validateRelayURLSecurity(s.relayURL); err != nil {
		return nil, err
	}
	cfg := loadShareRuntimeConfig()
	serverDesc, publicDesc, err := requestIssuedShareSession(ctx, s.relayURL, cfg)
	if err != nil {
		return nil, err
	}
	s.token = publicDesc.RoomID

	client, err := NewRelayClientWithDescriptor(s.relayURL, serverDesc, "server", s.meta)
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
		s.mu.Lock()
		fn := s.onConn
		if fn == nil {
			// Broker hasn't been created yet — cache the state so
			// OnConnected can replay it later.
			s.cachedConnState = &info
		}
		s.mu.Unlock()
		if fn != nil {
			fn(info)
		}
	})

	// Connect to relay
	if err := client.Connect(); err != nil {
		return nil, err
	}

	// Build connect URL
	connectURL := publicDesc.PublicConnectURL(s.relayURL)

	// Generate QR code
	qrStr, _ := QRCodeForURL(connectURL)
	qrLines, _ := QRCodeLines(connectURL)
	qrPNG, _ := QRCodePNG(connectURL)

	info := &SessionInfo{
		ConnectURL:          connectURL,
		Token:               publicDesc.RoomID,
		QRCode:              qrStr,
		QRCodePNG:           qrPNG,
		QRLines:             qrLines,
		ProtocolVersion:     publicDesc.ProtocolVersion,
		ShareMode:           publicDesc.ShareMode,
		CompatibilityNotice: publicDesc.Notice,
		RoomID:              publicDesc.RoomID,
		AuthExpiresAt:       publicDesc.AuthExpiresAt,
		RenewExpiresAt:      publicDesc.RenewExpiresAt,
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
	var cached *RelayConnectedState
	s.mu.Lock()
	s.onConn = fn
	cached = s.cachedConnState
	s.cachedConnState = nil
	s.mu.Unlock()
	// Replay the cached connected state that arrived before this handler
	// was registered (e.g. relay responded before NewBroker was called).
	if cached != nil && fn != nil {
		go fn(*cached)
	}
}

func (s *Session) Send(msg GatewayMessage) error {
	if s.client == nil {
		return fmt.Errorf("tunnel session: no relay client")
	}
	return s.client.Send(msg)
}

func (s *Session) SendActiveSession(sessionID string, authorityEpoch uint64, barrierEventID string, barrierOrdinal int64, projectionHash string) error {
	if s.client == nil {
		return fmt.Errorf("tunnel session: no relay client")
	}
	return s.client.SendActiveSession(sessionID, authorityEpoch, barrierEventID, barrierOrdinal, projectionHash)
}

func (s *Session) SendActiveSessionWithParams(sessionID string, authorityEpoch uint64, barrierEventID string, barrierOrdinal int64, projectionHash string, workspacePath, providerName, modelName string) error {
	if s.client == nil {
		return fmt.Errorf("tunnel session: no relay client")
	}
	return s.client.SendActiveSessionWithMode(sessionID, "", authorityEpoch, barrierEventID, barrierOrdinal, projectionHash, workspacePath, providerName, modelName)
}

func (s *Session) SendActiveSessionWithMode(sessionID, mode string, authorityEpoch uint64, barrierEventID string, barrierOrdinal int64, projectionHash string, workspacePath, providerName, modelName string) error {
	if s.client == nil {
		return fmt.Errorf("tunnel session: no relay client")
	}
	return s.client.SendActiveSessionWithMode(sessionID, mode, authorityEpoch, barrierEventID, barrierOrdinal, projectionHash, workspacePath, providerName, modelName)
}

func (s *Session) SendServerReady(authorityEpoch uint64) error {
	if s.client == nil {
		return fmt.Errorf("tunnel session: no relay client")
	}
	return s.client.SendServerReady(authorityEpoch)
}

func (s *Session) Info() *SessionInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSessionInfo(s.info)
}

func (s *Session) Stop() {
	if s.client != nil {
		s.client.Close()
	}
}

func (s *Session) RefreshInvite(ctx context.Context) (*SessionInfo, error) {
	if err := validateRelayURLSecurity(s.relayURL); err != nil {
		return nil, err
	}
	if s.client == nil {
		return nil, fmt.Errorf("tunnel session: no relay client")
	}
	serverDesc, publicDesc, err := refreshIssuedShareSession(ctx, s.relayURL, s.client.currentShareDescriptor())
	if err != nil {
		return nil, err
	}
	s.client.updateShareDescriptor(func(desc *ShareDescriptor) {
		*desc = serverDesc
	})
	connectURL := publicDesc.PublicConnectURL(s.relayURL)
	qrStr, _ := QRCodeForURL(connectURL)
	qrLines, _ := QRCodeLines(connectURL)
	qrPNG, _ := QRCodePNG(connectURL)
	info := &SessionInfo{
		ConnectURL:          connectURL,
		Token:               publicDesc.RoomID,
		QRCode:              qrStr,
		QRCodePNG:           qrPNG,
		QRLines:             qrLines,
		ProtocolVersion:     publicDesc.ProtocolVersion,
		ShareMode:           publicDesc.ShareMode,
		CompatibilityNotice: publicDesc.Notice,
		RoomID:              publicDesc.RoomID,
		AuthExpiresAt:       publicDesc.AuthExpiresAt,
		RenewExpiresAt:      publicDesc.RenewExpiresAt,
	}
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
	return cloneSessionInfo(info), nil
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

func cloneSessionInfo(info *SessionInfo) *SessionInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	cloned.QRCodePNG = append([]byte(nil), info.QRCodePNG...)
	cloned.QRLines = append([]string(nil), info.QRLines...)
	return &cloned
}
