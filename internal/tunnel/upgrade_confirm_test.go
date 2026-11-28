package tunnel

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// #1876 case 2 (host side): the host's DataChannel opened, the mobile never
// used it, and every downstream event vanished because the broker had switched
// to P2P-only on OnOpen alone. Until the mobile proves it reads the channel
// the broker must mirror to the relay; if it never does, revert to relay-only.

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// confirmTransport is a thread-safe fake DataChannel transport. When
// answerPong is set it replies to host keepalive pings like a current mobile.
type confirmTransport struct {
	mu         sync.Mutex
	sent       [][]byte
	onMsg      func([]byte)
	onDisc     func()
	answerPong atomic.Bool
	sendErr    atomic.Value // error
}

func (c *confirmTransport) Send(data []byte) error {
	if err, _ := c.sendErr.Load().(error); err != nil {
		return err
	}
	c.mu.Lock()
	c.sent = append(c.sent, data)
	h := c.onMsg
	c.mu.Unlock()
	var msg GatewayMessage
	if err := json.Unmarshal(data, &msg); err == nil && msg.Type == EventPing && c.answerPong.Load() && h != nil {
		pong, _ := json.Marshal(GatewayMessage{Type: CmdPong})
		h(pong)
	}
	return nil
}
func (c *confirmTransport) sentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}
func (c *confirmTransport) OnMessage(h func([]byte)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onMsg = h
}
func (c *confirmTransport) OnDisconnect(h func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onDisc = h
}
func (c *confirmTransport) Close() error      { return nil }
func (c *confirmTransport) IsConnected() bool { return true }

func TestBrokerUnconfirmedP2PMirrorsToRelayUntilMobileSpeaks(t *testing.T) {
	var mu sync.Mutex
	var relay []GatewayMessage
	b := NewBroker(nil)
	t.Cleanup(func() { b.Stop() })
	b.relaySender = func(msg GatewayMessage) error {
		mu.Lock()
		defer mu.Unlock()
		relay = append(relay, msg)
		return nil
	}
	p2p := &confirmTransport{}
	b.SetP2PTransport(p2p)

	var cmds []GatewayMessage
	b.OnCommand(func(cmd GatewayMessage) { cmds = append(cmds, cmd) })

	relayCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(relay)
	}

	first := GatewayMessage{Type: EventActivity, EventID: "ev-1"}
	b.enqueueOut(first)
	waitFor(t, "first event on P2P", func() bool { return p2p.sentCount() == 1 })
	waitFor(t, "first event mirrored to relay", func() bool { return relayCount() == 1 })
	if b.P2PConfirmed() {
		t.Fatal("channel must not be confirmed before the mobile sends anything")
	}

	// A keepalive answer is liveness evidence, not a command.
	pong, _ := json.Marshal(GatewayMessage{Type: CmdPong})
	b.HandleP2PMessage(pong)
	if !b.P2PConfirmed() {
		t.Fatal("upstream pong must confirm the channel")
	}
	if !b.p2pPongSupported() {
		t.Fatal("pong must mark the client as keepalive-capable")
	}
	if len(cmds) != 0 {
		t.Fatalf("pong leaked into the command handler: %+v", cmds)
	}

	// Confirmed: relay mirror is off.
	second := GatewayMessage{Type: EventActivity, EventID: "ev-2"}
	b.enqueueOut(second)
	waitFor(t, "second event on P2P", func() bool { return p2p.sentCount() == 2 })
	time.Sleep(100 * time.Millisecond)
	if n := relayCount(); n != 1 {
		t.Fatalf("confirmed channel must not mirror to relay, relay got %d events", n)
	}
}

func TestBrokerUpstreamWithoutTransportDoesNotConfirm(t *testing.T) {
	b := NewBroker(nil)
	defer b.Stop()
	msg, _ := json.Marshal(GatewayMessage{Type: EventUserMessage})
	b.HandleP2PMessage(msg)
	if b.P2PConfirmed() {
		t.Fatal("upstream traffic with no transport must not set the confirmed flag")
	}
	if b.P2PConfirmCh() != nil {
		t.Fatal("no transport => nil confirm channel")
	}
}

// confirmHarness runs a real UpgradeManager against a fake transport whose
// channel is already open, and returns once the broker installed it.
func confirmHarness(t *testing.T, cfg UpgradeConfig) (*UpgradeManager, *Broker, *confirmTransport) {
	return confirmHarnessWithRelay(t, cfg, nil)
}

func confirmHarnessWithRelay(t *testing.T, cfg UpgradeConfig, relaySender func(GatewayMessage) error) (*UpgradeManager, *Broker, *confirmTransport) {
	t.Helper()
	b := NewBroker(NewSession("wss://test.local"))
	t.Cleanup(func() { b.Stop() })
	b.relaySender = relaySender
	tr := &confirmTransport{}
	ready := make(chan struct{})
	close(ready)
	factory := func() (Transport, <-chan struct{}, func(func(SignalMessage), <-chan SignalMessage) error, func(), error) {
		return tr, ready, func(func(SignalMessage), <-chan SignalMessage) error { return nil }, func() {}, nil
	}
	cfg.Enabled = true
	// ICETimeout must exceed the 3s mobile-ready delay inside runUpgrade.
	cfg.ICETimeout = 10 * time.Second
	cfg.RetryDelay = time.Hour
	m := NewUpgradeManager(b, factory, cfg)
	m.mu.Lock()
	m.state = UpgradeNegotiating
	m.mu.Unlock()
	go m.runUpgrade(make(chan SignalMessage))
	waitFor(t, "P2P transport installed", func() bool { return b.HasP2PTransport() })
	return m, b, tr
}

func TestRunUpgradeStaysMirroredWhenMobileNeverSignals(t *testing.T) {
	// Older mobile builds never speak on the channel (heartbeat goes over the
	// relay WebSocket) while their DataChannel is perfectly healthy: silence
	// must not tear the channel down, and the relay mirror must stay on.
	var mu sync.Mutex
	var relay []GatewayMessage
	m, b, tr := confirmHarnessWithRelay(t, UpgradeConfig{ConfirmTimeout: 300 * time.Millisecond, KeepAliveInterval: 50 * time.Millisecond}, func(msg GatewayMessage) error {
		mu.Lock()
		defer mu.Unlock()
		relay = append(relay, msg)
		return nil
	})
	tr.answerPong.Store(false)
	relayCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(relay)
	}

	time.Sleep(3 * 300 * time.Millisecond) // well past ConfirmTimeout
	if !b.HasP2PTransport() {
		t.Fatal("silent-but-healthy channel must not be torn down on confirm timeout")
	}
	if b.P2PConfirmed() {
		t.Fatal("channel must still be unconfirmed")
	}
	if got := m.State(); got != UpgradeActive {
		t.Fatalf("expected UpgradeActive, got %v", got)
	}

	// Mirror still on: a new event reaches both paths.
	ev := GatewayMessage{Type: EventActivity, EventID: "ev-late"}
	b.enqueueOut(ev)
	waitFor(t, "late event mirrored to relay", func() bool { return relayCount() == 1 })
	waitFor(t, "late event on P2P", func() bool { return tr.sentCount() >= 1 })
}

func TestRunUpgradeStaysActiveWhenMobileSendsOnTheChannel(t *testing.T) {
	m, b, _ := confirmHarness(t, UpgradeConfig{ConfirmTimeout: 300 * time.Millisecond, KeepAliveInterval: 50 * time.Millisecond})

	up, _ := json.Marshal(GatewayMessage{Type: EventUserMessage, EventID: "u-1"})
	b.HandleP2PMessage(up)
	waitFor(t, "confirmation observed", func() bool {
		select {
		case <-b.P2PConfirmCh():
			return true
		default:
			return false
		}
	})
	// Old mobile: never answers keepalive pings -> nothing enforced.
	time.Sleep(900 * time.Millisecond)
	if !b.HasP2PTransport() {
		t.Fatal("channel with upstream traffic must stay P2P even without pong support")
	}
	if got := m.State(); got != UpgradeActive {
		t.Fatalf("expected UpgradeActive, got %v", got)
	}
}

func TestRunUpgradeRevertsOnKeepaliveMisses(t *testing.T) {
	m, b, tr := confirmHarness(t, UpgradeConfig{ConfirmTimeout: time.Second, KeepAliveInterval: 50 * time.Millisecond})
	tr.answerPong.Store(true)

	// The mobile supports pong: the first ping is answered, which both
	// confirms the channel and flags the capability.
	waitFor(t, "keepalive-capable mobile confirmed", func() bool { return b.P2PConfirmed() && b.p2pPongSupported() })
	if got := m.State(); got != UpgradeActive {
		t.Fatalf("expected UpgradeActive, got %v", got)
	}

	// Mobile goes silent (backgrounded / path died silently): pion still
	// reports Open, so only the keepalive can notice.
	tr.answerPong.Store(false)
	waitFor(t, "revert after keepalive miss streak", func() bool { return !b.HasP2PTransport() })
	if got := m.State(); got != UpgradeFailed {
		t.Fatalf("expected UpgradeFailed after keepalive misses, got %v", got)
	}
}

func TestRunUpgradeKeepaliveLoopSurvivesInitialMiss(t *testing.T) {
	m, b, tr := confirmHarness(t, UpgradeConfig{ConfirmTimeout: time.Second, KeepAliveInterval: 50 * time.Millisecond})

	// Old-style confirmation via traffic while the first probes go unanswered.
	up, _ := json.Marshal(GatewayMessage{Type: EventUserMessage, EventID: "u-1"})
	b.HandleP2PMessage(up)
	time.Sleep(300 * time.Millisecond)
	if b.p2pPongSupported() {
		t.Fatal("nothing answered yet; pong must not be flagged")
	}

	// The mobile starts answering later: the loop must still be probing.
	tr.answerPong.Store(true)
	waitFor(t, "keepalive loop still probing after initial misses", func() bool { return b.p2pPongSupported() })
	if !b.HasP2PTransport() || m.State() != UpgradeActive {
		t.Fatalf("channel must stay active, transport=%v state=%v", b.HasP2PTransport(), m.State())
	}
}

func TestRunUpgradeKeepaliveSendFailureReverts(t *testing.T) {
	m, b, tr := confirmHarness(t, UpgradeConfig{ConfirmTimeout: time.Second, KeepAliveInterval: 50 * time.Millisecond})

	up, _ := json.Marshal(GatewayMessage{Type: EventUserMessage, EventID: "u-1"})
	b.HandleP2PMessage(up)
	waitFor(t, "confirmed", b.P2PConfirmed)

	// Next keepalive write fails (channel torn down underneath us).
	tr.sendErr.Store(errors.New("dc closed"))
	waitFor(t, "revert after keepalive send error", func() bool { return !b.HasP2PTransport() })
	if got := m.State(); got != UpgradeFailed {
		t.Fatalf("expected UpgradeFailed after keepalive send failure, got %v", got)
	}
}
