package webrtc

import (
	"context"
	"fmt"
	"sync"
	"time"

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

// NewPeer creates a new WebRTC Peer with default ICE configuration.
func NewPeer() (*Peer, error) {
	pc, err := webrtc.NewPeerConnection(PeerConfig())
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
			// Gathering complete
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
		p.dcReady = true
		fn := p.onDCOpen
		p.mu.Unlock()
		debug.Log("webrtc", "data channel opened")
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

// WaitForReady blocks until the DataChannel opens or the timeout elapses.
func (p *Peer) WaitForReady(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.dcReadyCh:
		return true
	case <-timer.C:
		return false
	}
}

// Close terminates the PeerConnection and DataChannel.
func (p *Peer) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()

		if p.dc != nil {
			_ = p.dc.Close()
		}
		if p.pc != nil {
			_ = p.pc.Close()
		}
		close(p.dcReadyCh)
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

// KeepICEWarm starts a background goroutine that keeps the ICE connection
// alive by periodically sending a small ping over the DataChannel.
// This prevents NAT timeout from silently killing the P2P connection.
func (p *Peer) KeepICEWarm(ctx context.Context, interval time.Duration) {
	safego.Go("webrtc.iceKeepalive", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.mu.Lock()
				ready := p.dcReady
				p.mu.Unlock()
				if !ready {
					continue
				}
				// SCTP keepalive is handled internally by pion, but we
				// also send an application-level ping to detect dead
				// connections faster than the OS TCP timeout.
				_ = p.Send([]byte(`{"type":"rtc_ping"}`))
			}
		}
	})
}
