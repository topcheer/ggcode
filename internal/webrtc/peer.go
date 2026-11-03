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

const DataChannelLabel = "ggcode-tunnel"

type Peer struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel

	mu          sync.Mutex
	dcReady     bool
	closed      bool
	closeOnce   sync.Once
	dcReadyOnce sync.Once
	disconnOnce sync.Once

	onMessage      func(data []byte)
	onDisconnect   func()
	onICECandidate func(candidate string)
	onDCOpen       func()

	dcReadyCh chan struct{}

	remoteDescSet     bool
	pendingCandidates []webrtc.ICECandidateInit
}

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

func NewPeer() (*Peer, error) {
	settings := webrtc.SettingEngine{}
	settings.LoggerFactory = &pionLoggerFactory{}

	settings.SetICETimeouts(15*time.Second, 30*time.Second, 700*time.Millisecond)

	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(PeerConfig())
	if err != nil {
		return nil, fmt.Errorf("webrtc: create peer connection: %w", err)
	}

	p := &Peer{
		pc:        pc,
		dcReadyCh: make(chan struct{}),
	}

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

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		debug.Log("webrtc", "ICE connection state: %s", state.String())
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		debug.Log("webrtc", "peer connection state: %s", state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:

		case webrtc.PeerConnectionStateDisconnected:

			debug.Log("webrtc", "peer connection disconnected (transient; waiting for recovery or Failed)")
		case webrtc.PeerConnectionStateFailed:
			debug.Log("webrtc", "peer connection failed")
			if stateTriggersTeardown(state) {
				p.handleDisconnect()
			}
		case webrtc.PeerConnectionStateClosed:
			debug.Log("webrtc", "peer connection closed")
		}
	})
	return p, nil
}

func stateTriggersTeardown(state webrtc.PeerConnectionState) bool {
	return state == webrtc.PeerConnectionStateFailed
}

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

func (p *Peer) SetRemoteAnswer(sdp string) error {
	desc, err := decodeSDP(sdp)
	if err != nil {
		return fmt.Errorf("webrtc: decode answer: %w", err)
	}
	if err := p.pc.SetRemoteDescription(desc); err != nil {
		return fmt.Errorf("webrtc: set remote answer: %w", err)
	}
	p.markRemoteDescSet()
	return nil
}

func (p *Peer) SetRemoteOffer(sdp string) error {
	desc, err := decodeSDP(sdp)
	if err != nil {
		return fmt.Errorf("webrtc: decode offer: %w", err)
	}
	if err := p.pc.SetRemoteDescription(desc); err != nil {
		return fmt.Errorf("webrtc: set remote offer: %w", err)
	}
	p.markRemoteDescSet()

	p.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != DataChannelLabel {
			debug.Log("webrtc", "ignoring unexpected data channel: %s", dc.Label())
			return
		}
		p.attachDataChannel(dc)
	})
	return nil
}

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

func (p *Peer) AddICECandidate(candidateStr string) error {
	init, err := decodeCandidate(candidateStr)
	if err != nil {
		return fmt.Errorf("webrtc: decode candidate: %w", err)
	}
	p.mu.Lock()
	if !p.remoteDescSet {
		p.pendingCandidates = append(p.pendingCandidates, init)
		p.mu.Unlock()
		debug.Log("webrtc", "buffered ICE candidate received before remote description")
		return nil
	}
	p.mu.Unlock()
	return p.pc.AddICECandidate(init)
}

func (p *Peer) markRemoteDescSet() {
	p.mu.Lock()
	p.remoteDescSet = true
	pending := p.pendingCandidates
	p.pendingCandidates = nil
	p.mu.Unlock()
	for _, init := range pending {
		if err := p.pc.AddICECandidate(init); err != nil {
			debug.Log("webrtc", "replay buffered ICE candidate: %v", err)
		} else {
			debug.Log("webrtc", "replayed buffered ICE candidate")
		}
	}
}

func (p *Peer) OnICECandidate(fn func(candidate string)) {
	p.mu.Lock()
	p.onICECandidate = fn
	p.mu.Unlock()
}

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

		p.signalDCReady()
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

		p.mu.Lock()
		stale := p.dc != dc
		if !stale {
			p.dcReady = false
		}
		p.mu.Unlock()
		if stale {
			return
		}
		p.handleDisconnect()
	})

	dc.OnError(func(err error) {
		debug.Log("webrtc", "data channel error: %v", err)
		p.mu.Lock()
		stale := p.dc != dc
		if !stale {
			p.dcReady = false
		}
		p.mu.Unlock()
		if stale {
			return
		}
		p.handleDisconnect()
	})
}

func (p *Peer) OnMessage(fn func(data []byte)) {
	p.mu.Lock()
	p.onMessage = fn
	p.mu.Unlock()
}

func (p *Peer) OnDisconnect(fn func()) {
	p.mu.Lock()
	p.onDisconnect = fn
	p.mu.Unlock()
}

func (p *Peer) OnDCOpen(fn func()) {
	p.mu.Lock()
	p.onDCOpen = fn
	p.mu.Unlock()
}

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

func (p *Peer) IsReady() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dcReady
}

func (p *Peer) Close() error {
	p.closeOnce.Do(func() {

		p.mu.Lock()
		p.closed = true
		dc := p.dc
		p.mu.Unlock()

		if dc != nil {
			_ = dc.Close()
		}
		if p.pc != nil {
			_ = p.pc.Close()
		}

		p.signalDCReady()
	})
	return nil
}

func (p *Peer) signalDCReady() {
	p.dcReadyOnce.Do(func() {
		close(p.dcReadyCh)
	})
}

func (p *Peer) handleDisconnect() {
	p.disconnOnce.Do(func() {
		p.mu.Lock()
		fn := p.onDisconnect
		p.mu.Unlock()
		if fn != nil {
			safego.Go("webrtc.disconnect", fn)
		}
	})
}
