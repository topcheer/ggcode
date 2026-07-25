package webrtc

import (
	"fmt"
	"sync"
	"time"

	pionlogging "github.com/pion/logging"
	"github.com/pion/webrtc/v4"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// DataChannelLabel is the WebRTC DataChannel label used for the tunnel.
const DataChannelLabel = "ggcode-tunnel"

// Peer wraps a WebRTC PeerConnection with a DataChannel for tunnel transport.
// It implements the offerer (host) and answerer (mobile) roles.
type Peer struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel

	mu        sync.Mutex
	dcReady   bool
	closed    bool
	closeOnce sync.Once

	// Callbacks (set by caller before ICE completes)
	onMessage      func(data []byte)
	onDisconnect   func()
	onICECandidate func(candidate string)
	onDCOpen       func()

	// Completion signaling
	dcReadyCh chan struct{}
}

// pionLogger adapts pion/logging to our debug.Log system,
// preventing pion from writing directly to stderr (which breaks the TUI).
type pionLogger struct {
	scope string
}

func (l *pionLogger) Trace(msg string)                          {}
func (l *pionLogger) Tracef(format string, args ...interface{}) {}
func (l *pionLogger) Debug(msg string)                          {}
func (l *pionLogger) Debugf(format string, args ...interface{}) {}
func (l *pionLogger) Info(msg string)                           {}
func (l *pionLogger) Infof(format string, args ...interface{})  {}
func (l *pionLogger) Warn(msg string) {
	debug.Log("webrtc", "pion[%s] WARN: %s", l.scope, msg)
}
func (l *pionLogger) Warnf(format string, args ...interface{}) {
	debug.Log("webrtc", "pion[%s] WARN: "+format, append([]interface{}{l.scope}, args...)...)
}
func (l *pionLogger) Error(msg string) {
	debug.Log("webrtc", "pion[%s] ERROR: %s", l.scope, msg)
}
func (l *pionLogger) Errorf(format string, args ...interface{}) {
	debug.Log("webrtc", "pion[%s] ERROR: "+format, append([]interface{}{l.scope}, args...)...)
}

type pionLoggerFactory struct{}

func (f *pionLoggerFactory) NewLogger(scope string) pionlogging.LeveledLogger {
	return &pionLogger{scope: scope}
}

// NewPeer creates a new WebRTC Peer with default ICE configuration.
func NewPeer() (*Peer, error) {
	settings := webrtc.SettingEngine{}
	settings.LoggerFactory = &pionLoggerFactory{}
	// Give TURN/STUN servers more time to respond (default is ~5s which is
	// too short for networks behind GFW/CGNAT with UDP packet loss).
	settings.SetICETimeouts(15*time.Second, 30*time.Second, 700*time.Millisecond)

	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(PeerConfig())
	if err != nil {
		return nil, fmt.Errorf("webrtc: create peer connection: %w", err)
	}

	p := &Peer{
		pc:        pc,
		dcReadyCh: make(chan struct{}),
	}

	// Forward local ICE candidates to the caller for trickle ICE.
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			debug.Log("webrtc", "ICE gathering complete")
			return
		}
		init := candidate.ToJSON()
		candidateStr, err := encodeCandidate(init)
		if err != nil {
			debug.Log("webrtc", "OnICECandidate: encode error: %v", err)
			return
		}
		p.mu.Lock()
		fn := p.onICECandidate
		p.mu.Unlock()
		if fn != nil {
			fn(candidateStr)
		}
	})

	// Handle ICE connection state changes
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		debug.Log("webrtc", "ICE connection state: %s", state.String())
	})

	// Handle peer connection state changes
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		debug.Log("webrtc", "peer connection state: %s", state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			// P2P connected (DataChannel may open shortly)
		case webrtc.PeerConnectionStateDisconnected:
			debug.Log("webrtc", "peer connection disconnected")
			p.handleDisconnect()
		case webrtc.PeerConnectionStateFailed:
			debug.Log("webrtc", "peer connection failed")
			p.handleDisconnect()
		case webrtc.PeerConnectionStateClosed:
			debug.Log("webrtc", "peer connection closed")
		}
	})

	return p, nil
}

// ─── Host role (offerer) ───

// CreateOffer creates a DataChannel and generates an SDP offer.
// The host calls this to initiate the P2P upgrade.
func (p *Peer) CreateOffer() (string, error) {
	dc, err := p.pc.CreateDataChannel(DataChannelLabel, DataChannelConfig())
	if err != nil {
		return "", fmt.Errorf("webrtc: create data channel: %w", err)
	}
	p.attachDataChannel(dc)

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("webrtc: create offer: %w", err)
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("webrtc: set local description (offer): %w", err)
	}
	return encodeSDP(offer), nil
}

// SetRemoteAnswer sets the remote SDP answer from the mobile client.
func (p *Peer) SetRemoteAnswer(sdp string) error {
	desc, err := decodeSDP(sdp)
	if err != nil {
		return fmt.Errorf("webrtc: decode answer: %w", err)
	}
	if err := p.pc.SetRemoteDescription(desc); err != nil {
		return fmt.Errorf("webrtc: set remote answer: %w", err)
	}
	return nil
}

// ─── Mobile role (answerer) ───

// SetRemoteOffer sets the remote SDP offer from the host.
func (p *Peer) SetRemoteOffer(sdp string) error {
	desc, err := decodeSDP(sdp)
	if err != nil {
		return fmt.Errorf("webrtc: decode offer: %w", err)
	}
	if err := p.pc.SetRemoteDescription(desc); err != nil {
		return fmt.Errorf("webrtc: set remote offer: %w", err)
	}

	// Set up handler for the incoming DataChannel created by the host.
	p.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != DataChannelLabel {
			debug.Log("webrtc", "ignoring unexpected data channel: %s", dc.Label())
			return
		}
		p.attachDataChannel(dc)
	})
	return nil
}

// CreateAnswer generates an SDP answer after SetRemoteOffer.
func (p *Peer) CreateAnswer() (string, error) {
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("webrtc: create answer: %w", err)
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("webrtc: set local description (answer): %w", err)
	}
	return encodeSDP(answer), nil
}

// ─── ICE candidate exchange ───

// AddICECandidate adds a remote trickle ICE candidate received from the peer.
func (p *Peer) AddICECandidate(candidateStr string) error {
	init, err := decodeCandidate(candidateStr)
	if err != nil {
		return fmt.Errorf("webrtc: decode candidate: %w", err)
	}
	return p.pc.AddICECandidate(init)
}

// OnICECandidate registers a callback for local ICE candidates.
// Call this before CreateOffer or SetRemoteOffer.
func (p *Peer) OnICECandidate(fn func(candidate string)) {
	p.mu.Lock()
	p.onICECandidate = fn
	p.mu.Unlock()
}

// ─── DataChannel ───

func (p *Peer) attachDataChannel(dc *webrtc.DataChannel) {
	p.mu.Lock()
	p.dc = dc
	p.mu.Unlock()

	dc.OnOpen(func() {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		p.dcReady = true
		fn := p.onDCOpen
		p.mu.Unlock()
		debug.Log("webrtc", "data channel opened")
		// Signal readiness. Use a recover-free close guarded by closed flag:
		// Close() also closes this channel, but it sets p.closed=true first
		// under the same lock, so we won't double-close.
		close(p.dcReadyCh)
		if fn != nil {
			fn()
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.mu.Lock()
		fn := p.onMessage
		p.mu.Unlock()
		if fn != nil {
			fn(msg.Data)
		}
	})

	dc.OnClose(func() {
		debug.Log("webrtc", "data channel closed")
		p.handleDisconnect()
	})

	dc.OnError(func(err error) {
		debug.Log("webrtc", "data channel error: %v", err)
		p.handleDisconnect()
	})
}

// OnMessage sets the handler for incoming DataChannel messages.
func (p *Peer) OnMessage(fn func(data []byte)) {
	p.mu.Lock()
	p.onMessage = fn
	p.mu.Unlock()
}

// OnDisconnect sets the handler for P2P connection loss.
func (p *Peer) OnDisconnect(fn func()) {
	p.mu.Lock()
	p.onDisconnect = fn
	p.mu.Unlock()
}

// OnDCOpen sets the handler called when the DataChannel becomes ready.
func (p *Peer) OnDCOpen(fn func()) {
	p.mu.Lock()
	p.onDCOpen = fn
	p.mu.Unlock()
}

// Send writes data to the DataChannel.
func (p *Peer) Send(data []byte) error {
	p.mu.Lock()
	dc := p.dc
	ready := p.dcReady
	p.mu.Unlock()
	if !ready || dc == nil {
		return fmt.Errorf("webrtc: data channel not ready")
	}
	return dc.Send(data)
}

// IsReady returns whether the DataChannel is open and ready for data.
func (p *Peer) IsReady() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dcReady
}

// Close terminates the PeerConnection and DataChannel.
func (p *Peer) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		wasReady := p.dcReady
		p.mu.Unlock()

		if p.dc != nil {
			_ = p.dc.Close()
		}
		if p.pc != nil {
			_ = p.pc.Close()
		}
		// Only close dcReadyCh if it hasn't been closed by OnOpen already.
		if !wasReady {
			close(p.dcReadyCh)
		}
	})
	return nil
}

func (p *Peer) handleDisconnect() {
	p.mu.Lock()
	fn := p.onDisconnect
	p.mu.Unlock()
	if fn != nil {
		safego.Go("webrtc.disconnect", fn)
	}
}
