package tunnel

// #923 guard: every runUpgrade early-exit path must clear p2pNegotiating
// and trigger recovery replay, or a transient factory/startNeg failure
// permanently suppresses event sync.

import (
	"errors"
	"testing"
	"time"
)

func newUpgradeHarness(t *testing.T, factory PeerFactory) (*UpgradeManager, *Broker) {
	t.Helper()
	sess := NewSession("wss://test.local")
	b := NewBroker(sess)
	t.Cleanup(func() { b.Stop() })
	m := NewUpgradeManager(b, factory, UpgradeConfig{
		Enabled:    true,
		ICETimeout: 2 * time.Second,
	})
	return m, b
}

// TestRunUpgradeFactoryErrorRevertsNegotiating: the factory-error early
// return used to leave p2pNegotiating set with no retry scheduled.
func TestRunUpgradeFactoryErrorRevertsNegotiating(t *testing.T) {
	factory := func() (transport Transport, readyCh <-chan struct{}, startNegotiation func(func(SignalMessage), <-chan SignalMessage) error, cleanup func(), err error) {
		return nil, nil, nil, nil, errors.New("factory boom")
	}
	m, b := newUpgradeHarness(t, factory)

	b.p2pNegotiating.Store(true)
	m.runUpgrade(make(chan SignalMessage))

	if b.p2pNegotiating.Load() {
		t.Fatal("factory error left p2pNegotiating set - recovery replay stays suppressed")
	}
	if m.stateLocked() != UpgradeFailed {
		t.Fatalf("expected UpgradeFailed, got %v", m.stateLocked())
	}
}

// TestRunUpgradeStartNegErrorRevertsNegotiating: same for startNeg.
func TestRunUpgradeStartNegErrorRevertsNegotiating(t *testing.T) {
	factory := func() (Transport, <-chan struct{}, func(func(SignalMessage), <-chan SignalMessage) error, func(), error) {
		startNeg := func(func(SignalMessage), <-chan SignalMessage) error {
			return errors.New("negotiation boom")
		}
		cleanup := func() {}
		return nil, nil, startNeg, cleanup, nil
	}
	m, b := newUpgradeHarness(t, factory)

	b.p2pNegotiating.Store(true)
	m.runUpgrade(make(chan SignalMessage))

	if b.p2pNegotiating.Load() {
		t.Fatal("startNeg error left p2pNegotiating set - recovery replay stays suppressed")
	}
	if m.stateLocked() != UpgradeFailed {
		t.Fatalf("expected UpgradeFailed, got %v", m.stateLocked())
	}
}
