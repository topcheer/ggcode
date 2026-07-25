package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// UpgradeState tracks the P2P upgrade lifecycle.
type UpgradeState int

const (
	UpgradeIdle UpgradeState = iota
	UpgradeNegotiating
	UpgradeActive
	UpgradeFailed
)

func (s UpgradeState) String() string {
	switch s {
	case UpgradeIdle:
		return "idle"
	case UpgradeNegotiating:
		return "negotiating"
	case UpgradeActive:
		return "active"
	case UpgradeFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// UpgradeConfig controls the P2P upgrade behavior.
type UpgradeConfig struct {
	// Enabled gates the entire P2P upgrade. When false, the manager stays idle.
	Enabled bool
	// ICETimeout is the maximum time to wait for ICE to complete.
	ICETimeout time.Duration
	// KeepAliveInterval is how often to send application-level pings over DC.
	KeepAliveInterval time.Duration
	// RetryDelay is the cooldown before re-attempting after a failure.
	RetryDelay time.Duration
}

// DefaultUpgradeConfig returns production-ready defaults.
// P2P is enabled by default; users can opt out via config.
func DefaultUpgradeConfig() UpgradeConfig {
	return UpgradeConfig{
		Enabled:           true,
		ICETimeout:        25 * time.Second, // includes 1s settle delay + offer retries
		KeepAliveInterval: 20 * time.Second,
		RetryDelay:        30 * time.Second,
	}
}

// PeerFactory creates a new WebRTC PeerConnection and returns it as a Transport.
// This is injected by the webrtc package to avoid a circular dependency.
// Returns:
//   - transport: the DataChannel transport (implements tunnel.Transport)
//   - readyCh: closed when the DataChannel becomes ready for use
//   - startNegotiation: begins SDP/ICE exchange. sendSignal delivers outbound
//     signals (offer, local ICE candidates) to the peer via the relay.
//     recv delivers inbound signals (answer, remote ICE candidates) from the peer.
//   - cleanup: function to call when the P2P connection is torn down
type PeerFactory func() (
	transport Transport,
	readyCh <-chan struct{},
	startNegotiation func(sendSignal func(SignalMessage), recv <-chan SignalMessage) error,
	cleanup func(),
	err error,
)

// SignalMessage carries a WebRTC signaling message over the relay channel.
type SignalMessage struct {
	Type      string `json:"type"` // rtc_offer, rtc_answer, rtc_candidate
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// UpgradeManager coordinates the automatic upgrade from relay WebSocket
// to a P2P WebRTC DataChannel.
type UpgradeManager struct {
	cfg     UpgradeConfig
	broker  *Broker
	factory PeerFactory

	mu         sync.Mutex
	state      UpgradeState
	cancelNeg  context.CancelFunc // cancels ongoing negotiation
	generation uint64             // incremented on each Restart to invalidate stale callbacks

	// signalCh carries signaling messages from the relay to the active
	// negotiation. It is replaced on each Restart so stale goroutines
	// cannot consume signals meant for the new peer.
	signalCh chan SignalMessage

	// restartAt is the earliest time a Restart can actually execute.
	// Used for debouncing rapid reconnect events.
	restartAt time.Time

	// onStateChange is an optional callback for UI status updates.
	onStateChange func(UpgradeState)
}

// NewUpgradeManager creates a manager for the given broker.
func NewUpgradeManager(broker *Broker, factory PeerFactory, cfg UpgradeConfig) *UpgradeManager {
	return &UpgradeManager{
		cfg:      cfg,
		broker:   broker,
		factory:  factory,
		signalCh: make(chan SignalMessage, 64),
	}
}

// OnStateChange registers a callback for upgrade state transitions.
func (m *UpgradeManager) OnStateChange(fn func(UpgradeState)) {
	m.mu.Lock()
	m.onStateChange = fn
	m.mu.Unlock()
}

// State returns the current upgrade state.
func (m *UpgradeManager) State() UpgradeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Start begins the P2P upgrade process. This should be called after the
// relay connection is established and authenticated.
func (m *UpgradeManager) Start() {
	if !m.cfg.Enabled {
		return
	}
	m.mu.Lock()
	if m.state == UpgradeNegotiating || m.state == UpgradeActive {
		m.mu.Unlock()
		return
	}
	m.state = UpgradeNegotiating
	signalCh := m.signalCh
	m.mu.Unlock()

	m.notifyState()

	safego.Go("tunnel.upgrade.start", func() {
		m.runUpgrade(signalCh)
	})
}

// Restart cancels any ongoing P2P negotiation and starts a fresh one.
// Called when the mobile client reconnects to ensure the SDP offer reaches
// the newly established relay connection. If P2P is already active
// (DataChannel open), it is left untouched.
//
// Debounce: if called within 2s of a previous Restart, the call is
// coalesced into the pending restart to avoid tearing down peers when
// the relay fires multiple connect events in rapid succession.
func (m *UpgradeManager) Restart() {
	if !m.cfg.Enabled {
		return
	}
	m.mu.Lock()
	if m.state == UpgradeActive {
		m.mu.Unlock()
		return // P2P is working, don't disrupt it
	}

	now := time.Now()
	if now.Before(m.restartAt) {
		// A restart is already pending; coalesce.
		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: restart debounced (pending)")
		return
	}
	// Schedule the next restart window.
	m.restartAt = now.Add(5 * time.Second)

	// Cancel any stale negotiation so its callbacks are ignored.
	if m.cancelNeg != nil {
		m.cancelNeg()
	}
	// Replace signalCh so stale signal processors stop receiving.
	m.signalCh = make(chan SignalMessage, 64)
	m.generation++
	m.state = UpgradeNegotiating
	gen := m.generation
	signalCh := m.signalCh
	m.mu.Unlock()

	debug.Log("tunnel", "upgrade: restarting P2P negotiation (gen=%d, mobile reconnected)", gen)
	m.notifyState()

	safego.Go("tunnel.upgrade.restart", func() {
		m.runUpgrade(signalCh)
	})
}

// HandleSignalMessage routes a signaling message from the relay to the
// P2P negotiation. This is called when the broker receives rtc_* messages.
func (m *UpgradeManager) HandleSignalMessage(msg SignalMessage) {
	m.mu.Lock()
	ch := m.signalCh
	m.mu.Unlock()
	select {
	case ch <- msg:
	default:
		debug.Log("tunnel", "upgrade: signal channel full, dropping %s", msg.Type)
	}
}

func (m *UpgradeManager) runUpgrade(signalCh chan SignalMessage) {
	debug.Log("tunnel", "upgrade: starting P2P negotiation")
	m.broker.p2pNegotiating.Store(true)
	// Note: p2pNegotiating stays true through P2P disconnect. It's cleared
	// when P2P fails and we revert to relay (allowing recovery replay).

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ICETimeout)
	defer cancel()

	m.mu.Lock()
	m.cancelNeg = cancel
	gen := m.generation
	m.mu.Unlock()

	// Wait for the mobile client to complete key exchange and become "ready"
	// in the relay. The relay gateway only forwards server broadcasts to
	// "ready" clients. If the offer is sent before the mobile is ready, it
	// is silently dropped. 3s accounts for key exchange + relay round trips.
	select {
	case <-time.After(3 * time.Second):
		debug.Log("tunnel", "upgrade: mobile-ready delay elapsed")
	case <-ctx.Done():
		return
	}

	// Check if we've been superseded by a newer restart.
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: stale generation after delay, aborting")
		return
	}
	m.mu.Unlock()

	// Create the peer via factory.
	p2pTransport, readyCh, startNeg, cleanup, err := m.factory()
	if err != nil {
		debug.Log("tunnel", "upgrade: factory error: %v", err)
		m.setState(UpgradeFailed)
		return
	}

	defer cleanup()

	// staleGen returns true if a newer negotiation has started.
	staleGen := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.generation != gen
	}

	p2pDone := make(chan struct{}) // closed when P2P disconnects or is cancelled
	var p2pDoneOnce sync.Once

	// Wire disconnect handler.
	p2pTransport.OnDisconnect(func() {
		if staleGen() {
			return // stale peer from a previous negotiation
		}
		debug.Log("tunnel", "upgrade: P2P disconnected, reverting to relay")
		m.broker.SetP2PTransport(nil)
		m.setState(UpgradeFailed)
		p2pDoneOnce.Do(func() { close(p2pDone) })
		// Schedule retry
		safego.Go("tunnel.upgrade.retry", func() {
			select {
			case <-time.After(m.cfg.RetryDelay):
			case <-ctx.Done():
				return
			}
			m.Start()
		})
	})

	// Wire incoming DataChannel messages: route to broker's command handler,
	// same as relay session messages. RTC signals from mobile (rtc_answer,
	// rtc_candidate) are intercepted by the broker's OnCommand wrapper.
	p2pTransport.OnMessage(func(data []byte) {
		m.broker.HandleP2PMessage(data)
	})

	// Wait for DataChannel ready in a background goroutine.
	safego.Go("tunnel.upgrade.waitReady", func() {
		select {
		case <-readyCh:
			if staleGen() {
				debug.Log("tunnel", "upgrade: DataChannel ready but stale, discarding")
				return
			}
			debug.Log("tunnel", "upgrade: DataChannel ready, switching transport")
			m.broker.SetP2PTransport(p2pTransport)
			// Now that P2P is active, sync any messages that mobile missed
			// during P2P negotiation. This sends only the incremental events
			// that relay doesn't have yet (relay history lags behind because
			// we skipped recovery replay during P2P negotiation).
			m.broker.SyncP2PReplay()
			m.setState(UpgradeActive)
		case <-ctx.Done():
			return
		}
	})

	// Start the negotiation (creates SDP offer/answer, begins ICE gathering).
	// Outbound signals are routed through the broker to the relay.
	// Inbound signals arrive on m.signalCh from HandleSignalMessage.
	sendSignal := func(signal SignalMessage) {
		if err := m.broker.SendSignal(signal); err != nil {
			debug.Log("tunnel", "upgrade: send signal error: %v", err)
		}
	}
	recvCh := (<-chan SignalMessage)(signalCh)
	if err := startNeg(sendSignal, recvCh); err != nil {
		debug.Log("tunnel", "upgrade: negotiation start error: %v", err)
		if !staleGen() {
			m.setState(UpgradeFailed)
		}
		return
	}

	// Wait for P2P disconnect. Once active, ignore ICE timeout —
	// the DataChannel should persist until network disconnect or Stop().
	select {
	case <-p2pDone:
		// P2P disconnected, OnDisconnect already handled cleanup.
	case <-ctx.Done():
		if m.state == UpgradeActive {
			// P2P is active — wait for disconnect instead of timing out.
			debug.Log("tunnel", "upgrade: ctx done but P2P active, waiting for disconnect")
			<-p2pDone
			m.broker.p2pNegotiating.Store(false)
			m.broker.TriggerReplayNow()
		} else if ctx.Err() == context.DeadlineExceeded {
			debug.Log("tunnel", "upgrade: ICE timeout, staying on relay")
			m.broker.p2pNegotiating.Store(false)
			m.broker.TriggerReplayNow()
			m.setState(UpgradeFailed)
		}
	}
}

func (m *UpgradeManager) setState(s UpgradeState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
	m.notifyState()
}

func (m *UpgradeManager) notifyState() {
	m.mu.Lock()
	fn := m.onStateChange
	state := m.state
	m.mu.Unlock()
	if fn != nil {
		safego.Go("tunnel.upgrade.stateChange", func() { fn(state) })
	}
}

// Stop cancels any ongoing upgrade negotiation and tears down P2P.
func (m *UpgradeManager) Stop() {
	m.mu.Lock()
	cancel := m.cancelNeg
	m.cancelNeg = nil
	m.state = UpgradeIdle
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.broker.SetP2PTransport(nil)
}

// EncodeSignalMessage creates a GatewayMessage wrapping signaling data
// for transmission over the relay WebSocket.
func EncodeSignalMessage(signal SignalMessage) (GatewayMessage, error) {
	var eventType string
	switch signal.Type {
	case "rtc_offer":
		eventType = EventRTCOffer
	case "rtc_answer":
		eventType = EventRTCAnswer
	case "rtc_candidate":
		eventType = EventRTCCandidate
	default:
		return GatewayMessage{}, fmt.Errorf("unknown signal type: %s", signal.Type)
	}
	data, err := json.Marshal(signal)
	if err != nil {
		return GatewayMessage{}, err
	}
	return GatewayMessage{Type: eventType, Data: data}, nil
}

// DecodeSignalMessage extracts signaling data from a GatewayMessage.
func DecodeSignalMessage(msg GatewayMessage) (SignalMessage, bool) {
	switch msg.Type {
	case EventRTCOffer, EventRTCAnswer, EventRTCCandidate, EventRTCConnected, EventRTCFailed:
	default:
		return SignalMessage{}, false
	}
	var signal SignalMessage
	if err := json.Unmarshal(msg.Data, &signal); err != nil {
		return SignalMessage{}, false
	}
	if msg.Type == EventRTCOffer {
		signal.Type = "rtc_offer"
	} else if msg.Type == EventRTCAnswer {
		signal.Type = "rtc_answer"
	} else if msg.Type == EventRTCCandidate {
		signal.Type = "rtc_candidate"
	}
	return signal, true
}

// IsRTCSignalMessage returns true if a GatewayMessage is a WebRTC signaling message.
func IsRTCSignalMessage(msg GatewayMessage) bool {
	switch msg.Type {
	case EventRTCOffer, EventRTCAnswer, EventRTCCandidate, EventRTCConnected, EventRTCFailed:
		return true
	}
	return false
}
