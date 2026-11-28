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

	// ConfirmTimeout bounds how long the host mirrors every event to the
	// relay after its DataChannel opened while waiting for the first upstream
	// message from the mobile. Exceeding it means the mobile never (or no
	// longer) reads the channel: the host reverts to relay-only.
	ConfirmTimeout time.Duration
}

const (
	defaultKeepAliveInterval = 20 * time.Second
	defaultConfirmTimeout    = 45 * time.Second
	// After an *unconfirmed* revert (mobile never used the channel) wait this
	// long before negotiating again, so a client that cannot do P2P does not
	// make the host flap every RetryDelay.
	unconfirmedRetryDelay = 2 * time.Minute
	p2pKeepaliveMaxMisses = 3
)

func DefaultUpgradeConfig() UpgradeConfig {
	return UpgradeConfig{
		Enabled:           true,
		ICETimeout:        25 * time.Second,
		KeepAliveInterval: defaultKeepAliveInterval,
		RetryDelay:        30 * time.Second,
		ConfirmTimeout:    defaultConfirmTimeout,
	}
}

func (c UpgradeConfig) keepAliveInterval() time.Duration {
	if c.KeepAliveInterval > 0 {
		return c.KeepAliveInterval
	}
	return defaultKeepAliveInterval
}

func (c UpgradeConfig) confirmTimeout() time.Duration {
	if c.ConfirmTimeout > 0 {
		return c.ConfirmTimeout
	}
	return defaultConfirmTimeout
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

	// backoffUntil suppresses Start/Restart after an unconfirmed revert.
	backoffUntil time.Time

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
	if now := time.Now(); now.Before(m.backoffUntil) {
		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: Start deferred (backoff %s)", time.Until(m.backoffUntil).Round(time.Second))
		return
	}
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
	if now := time.Now(); now.Before(m.backoffUntil) {
		m.mu.Unlock()
		debug.Log("tunnel", "upgrade: Restart deferred (backoff %s)", time.Until(m.backoffUntil).Round(time.Second))
		return
	}
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
	var reverted atomic.Bool

	// revertToRelay tears the channel down and makes the relay the only
	// transport again. Idempotent: the transport's OnDisconnect and our own
	// watchdogs (confirmation timeout / keepalive misses) share it.
	revertToRelay := func(reason string, retryAfter time.Duration) {
		if !reverted.CompareAndSwap(false, true) {
			p2pDoneOnce.Do(func() { close(p2pDone) })
			return
		}
		debug.Log("tunnel", "upgrade: reverting to relay (%s)", reason)
		m.broker.SetP2PTransport(nil)
		m.broker.p2pNegotiating.Store(false)
		m.broker.TriggerReplayNow()
		if !staleGen() && !m.stopped.Load() {
			m.setState(UpgradeFailed)
		}
		p2pDoneOnce.Do(func() { close(p2pDone) })
		safego.Go("tunnel.upgrade.retry", func() {
			// Not bound to the negotiation ctx (ICETimeout): the revert is a
			// deliberate decision and RetryDelay/backoff regularly exceed it.
			timer := time.NewTimer(retryAfter)
			defer timer.Stop()
			<-timer.C
			if m.stopped.Load() {
				debug.Log("tunnel", "upgrade: retry aborted, manager stopped")
				return
			}
			m.Start()
		})
	}

	p2pTransport.OnDisconnect(func() {
		if reverted.Load() {
			p2pDoneOnce.Do(func() { close(p2pDone) })
			return
		}
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
			debug.Log("tunnel", "upgrade: DataChannel ready, switching transport (mirrored to relay until the mobile confirms)")
			m.broker.SetP2PTransport(p2pTransport)

			m.broker.SyncP2PReplay()
			m.setState(UpgradeActive)
			m.awaitConfirmation(p2pDone, &reverted, revertToRelay, staleGen)
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

// awaitConfirmation guards the window between the host's DataChannel opening
// and the mobile actually using it. Until the first upstream message arrives
// the broker mirrors every event to the relay; if nothing arrives within
// ConfirmTimeout the channel is torn down (relay-only) and re-negotiation is
// backed off. From the moment the channel is installed the host probes it
// with a ping every KeepAliveInterval: mobile builds that answer `pong` on
// the channel confirm themselves (and, once confirmed, a miss streak tears a
// silently dead channel down - pion keeps reporting Open in that case and
// Send never fails); older builds only confirm through their own traffic.
func (m *UpgradeManager) awaitConfirmation(p2pDone <-chan struct{}, reverted *atomic.Bool, revertToRelay func(string, time.Duration), staleGen func() bool) {
	confirmCh := m.broker.P2PConfirmCh()
	if confirmCh == nil {
		return
	}
	interval := m.cfg.keepAliveInterval()

	safego.Go("tunnel.upgrade.confirm", func() {
		select {
		case <-confirmCh:
			debug.Log("tunnel", "upgrade: P2P confirmed by mobile")
		case <-time.After(m.cfg.confirmTimeout()):
			if staleGen() || m.stopped.Load() || reverted.Load() {
				return
			}
			m.mu.Lock()
			m.backoffUntil = time.Now().Add(unconfirmedRetryDelay)
			m.mu.Unlock()
			revertToRelay("mobile never used the DataChannel (confirm timeout)", unconfirmedRetryDelay)
			return
		case <-p2pDone:
			return
		}
	})

	misses := 0
	safego.Go("tunnel.upgrade.keepalive", func() {
		probe := func() bool {
			before := m.broker.p2pPongs()
			if err := m.broker.SendP2PKeepalive(); err != nil {
				debug.Log("tunnel", "upgrade: keepalive send failed: %v", err)
				revertToRelay("keepalive send failed", m.cfg.RetryDelay)
				return false
			}
			// Give the pong a third of the interval, then judge.
			deadline := time.After(interval / 3)
			for {
				if m.broker.p2pPongs() > before {
					return true
				}
				select {
				case <-deadline:
					return false
				case <-p2pDone:
					return false
				default:
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
		// First probe right away so a pong-capable mobile confirms well inside
		// ConfirmTimeout even when KeepAliveInterval is close to it. A miss
		// here is not judged (older clients never answer); a send failure has
		// already reverted and the loop exits on the next iteration.
		time.Sleep(200 * time.Millisecond)
		if staleGen() || m.stopped.Load() || reverted.Load() {
			return
		}
		probe()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-p2pDone:
				return
			case <-ticker.C:
			}
			if staleGen() || m.stopped.Load() || reverted.Load() {
				return
			}
			if probe() {
				misses = 0
				continue
			}
			if !m.broker.p2pPongSupported() {
				// Older mobile builds never answer: liveness is only proven by
				// their own traffic, nothing to enforce here.
				continue
			}
			misses++
			debug.Log("tunnel", "upgrade: keepalive missed (%d/%d)", misses, p2pKeepaliveMaxMisses)
			if misses >= p2pKeepaliveMaxMisses {
				revertToRelay("keepalive misses (mobile stopped answering on the DataChannel)", m.cfg.RetryDelay)
				return
			}
		}
	})
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
