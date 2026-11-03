package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

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

type UpgradeConfig struct {
	Enabled bool

	ICETimeout time.Duration

	KeepAliveInterval time.Duration

	RetryDelay time.Duration
}

func DefaultUpgradeConfig() UpgradeConfig {
	return UpgradeConfig{
		Enabled:           true,
		ICETimeout:        25 * time.Second,
		KeepAliveInterval: 20 * time.Second,
		RetryDelay:        30 * time.Second,
	}
}

type PeerFactory func() (
	transport Transport,
	readyCh <-chan struct{},
	startNegotiation func(sendSignal func(SignalMessage), recv <-chan SignalMessage) error,
	cleanup func(),
	err error,
)

type SignalMessage struct {
	Type      string `json:"type"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

type UpgradeManager struct {
	cfg     UpgradeConfig
	broker  *Broker
	factory PeerFactory

	mu         sync.Mutex
	state      UpgradeState
	cancelNeg  context.CancelFunc
	generation uint64

	signalCh chan SignalMessage

	restartAt time.Time

	stopped atomic.Bool

	onStateChange func(UpgradeState)
}

func NewUpgradeManager(broker *Broker, factory PeerFactory, cfg UpgradeConfig) *UpgradeManager {
	return &UpgradeManager{
		cfg:      cfg,
		broker:   broker,
		factory:  factory,
		signalCh: make(chan SignalMessage, 64),
	}
}

func (m *UpgradeManager) OnStateChange(fn func(UpgradeState)) {
	m.mu.Lock()
	m.onStateChange = fn
	m.mu.Unlock()
}

func (m *UpgradeManager) State() UpgradeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *UpgradeManager) Start() {
	if !m.cfg.Enabled {
		return
	}
	if m.stopped.Load() {
		debug.Log("tunnel", "upgrade: Start ignored, manager stopped")
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

func (m *UpgradeManager) Restart() {
	if !m.cfg.Enabled {
		return
	}
	if m.stopped.Load() {
		debug.Log("tunnel", "upgrade: Restart ignored, manager stopped")
		return
	}
	m.mu.Lock()
	if m.state == UpgradeActive {
		m.mu.Unlock()
		return
	}

	now := time.Now()
	if now.Before(m.restartAt) {

		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: restart debounced (pending)")
		return
	}

	m.restartAt = now.Add(5 * time.Second)

	if m.cancelNeg != nil {
		m.cancelNeg()
	}

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

	m.mu.Lock()
	negGen := m.generation
	m.mu.Unlock()
	failAndRevert := func(reason string) {
		debug.Log("tunnel", "upgrade: reverting to relay (%s)", reason)
		m.broker.p2pNegotiating.Store(false)
		m.broker.TriggerReplayNow()
		if !m.staleGen(negGen) {
			m.setState(UpgradeFailed)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ICETimeout)
	defer cancel()

	m.mu.Lock()
	m.cancelNeg = cancel
	gen := m.generation
	m.mu.Unlock()

	select {
	case <-time.After(3 * time.Second):
		debug.Log("tunnel", "upgrade: mobile-ready delay elapsed")
	case <-ctx.Done():
		failAndRevert("ready-delay ctx done")
		return
	}

	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: stale generation after delay, aborting")
		return
	}
	m.mu.Unlock()

	p2pTransport, readyCh, startNeg, cleanup, err := m.factory()
	if err != nil {
		debug.Log("tunnel", "upgrade: factory error: %v", err)
		failAndRevert("factory error")
		return
	}

	defer cleanup()

	staleGen := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.generation != gen
	}

	p2pDone := make(chan struct{})
	var p2pDoneOnce sync.Once

	p2pTransport.OnDisconnect(func() {
		if staleGen() {
			return
		}
		debug.Log("tunnel", "upgrade: P2P disconnected, reverting to relay")
		wasActive := m.stateLocked() == UpgradeActive
		m.broker.SetP2PTransport(nil)

		m.broker.p2pNegotiating.Store(false)
		m.broker.TriggerReplayNow()

		if !staleGen() && !m.stopped.Load() {
			m.setState(UpgradeFailed)
		}
		p2pDoneOnce.Do(func() { close(p2pDone) })

		safego.Go("tunnel.upgrade.retry", func() {

			if wasActive {
				time.Sleep(m.cfg.RetryDelay)
			} else {
				select {
				case <-time.After(m.cfg.RetryDelay):
				case <-ctx.Done():
					return
				}
			}
			if m.stopped.Load() {
				debug.Log("tunnel", "upgrade: retry aborted, manager stopped")
				return
			}
			m.Start()
		})
	})

	p2pTransport.OnMessage(func(data []byte) {
		m.broker.HandleP2PMessage(data)
	})

	safego.Go("tunnel.upgrade.waitReady", func() {
		select {
		case <-readyCh:
			if staleGen() || m.stopped.Load() {

				debug.Log("tunnel", "upgrade: DataChannel ready but stale/stopped, discarding")
				return
			}
			debug.Log("tunnel", "upgrade: DataChannel ready, switching transport")
			m.broker.SetP2PTransport(p2pTransport)

			m.broker.SyncP2PReplay()
			m.setState(UpgradeActive)
		case <-ctx.Done():
			return
		}
	})

	sendSignal := func(signal SignalMessage) {
		if err := m.broker.SendSignal(signal); err != nil {
			debug.Log("tunnel", "upgrade: send signal error: %v", err)
		}
	}
	recvCh := (<-chan SignalMessage)(signalCh)
	if err := startNeg(sendSignal, recvCh); err != nil {
		debug.Log("tunnel", "upgrade: negotiation start error: %v", err)
		failAndRevert("startNeg error")
		return
	}

	select {
	case <-p2pDone:

	case <-ctx.Done():

		state := m.stateLocked()
		if state == UpgradeActive {

			debug.Log("tunnel", "upgrade: ctx done but P2P active, waiting for disconnect")
			<-p2pDone
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

func (m *UpgradeManager) stateLocked() UpgradeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *UpgradeManager) staleGen(gen uint64) bool {
	m.mu.Lock()
	cur := m.generation
	m.mu.Unlock()
	return cur != gen
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

func (m *UpgradeManager) Stop() {
	m.stopped.Store(true)
	m.mu.Lock()
	cancel := m.cancelNeg
	m.cancelNeg = nil
	m.state = UpgradeIdle
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.broker.SetP2PTransport(nil)

	m.broker.p2pNegotiating.Store(false)
}

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

func IsRTCSignalMessage(msg GatewayMessage) bool {
	switch msg.Type {
	case EventRTCOffer, EventRTCAnswer, EventRTCCandidate, EventRTCConnected, EventRTCFailed:
		return true
	}
	return false
}
