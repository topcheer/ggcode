package tunnel

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func activeDropHarness(t *testing.T, iceTimeout, retryDelay time.Duration) (*UpgradeManager, *Broker, *mockTransport, *int32) {
	t.Helper()
	var calls int32
	mock := &mockTransport{}
	sess := NewSession("wss://test.local")
	b := NewBroker(sess)
	t.Cleanup(func() { b.Stop() })
	ready := make(chan struct{})
	close(ready)
	factory := func() (Transport, <-chan struct{}, func(func(SignalMessage), <-chan SignalMessage) error, func(), error) {
		n := atomic.AddInt32(&calls, 1)
		if n > 1 {

			return nil, nil, nil, nil, errors.New("retry observed")
		}
		startNeg := func(func(SignalMessage), <-chan SignalMessage) error { return nil }
		return mock, ready, startNeg, func() {}, nil
	}
	m := NewUpgradeManager(b, factory, UpgradeConfig{
		Enabled:    true,
		ICETimeout: iceTimeout,
		RetryDelay: retryDelay,
	})
	return m, b, mock, &calls
}

func waitForState(t *testing.T, m *UpgradeManager, want UpgradeState) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m.stateLocked() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state did not reach %v in time, got %v", want, m.stateLocked())
}

func TestUpgradeActiveDropAfterCtxExpiryStillRetries(t *testing.T) {

	m, _, mock, calls := activeDropHarness(t, 4*time.Second, 30*time.Millisecond)
	t.Cleanup(func() { m.Stop() })

	go m.runUpgrade(make(chan SignalMessage, 1))
	waitForState(t, m, UpgradeActive)

	time.Sleep(1200 * time.Millisecond)
	if m.stateLocked() != UpgradeActive {
		t.Fatalf("expected still Active, got %v", m.stateLocked())
	}
	mock.onDisc()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(calls) >= 2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("retry Start() never re-invoked the factory after an active-session drop (session stranded on relay)")
}

func TestUpgradeDisconnectClearsNegotiatingHandler(t *testing.T) {

	m, b, mock, _ := activeDropHarness(t, 10*time.Second, 30*time.Millisecond)
	t.Cleanup(func() { m.Stop() })

	go m.runUpgrade(make(chan SignalMessage, 1))
	waitForState(t, m, UpgradeActive)

	if !b.p2pNegotiating.Load() {
		t.Fatal("expected p2pNegotiating true while active")
	}
	mock.onDisc()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !b.p2pNegotiating.Load() && m.stateLocked() == UpgradeFailed {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("disconnect handler did not clear p2pNegotiating/Failed state: negotiating=%v state=%v",
		b.p2pNegotiating.Load(), m.stateLocked())
}

func TestUpgradeStopClearsNegotiating(t *testing.T) {
	m, b, _, _ := activeDropHarness(t, 2*time.Second, time.Hour)
	b.p2pNegotiating.Store(true)
	m.Stop()
	if b.p2pNegotiating.Load() {
		t.Fatal("Stop() left p2pNegotiating set - broker recovery replay stays suppressed")
	}
}

func TestUpgradeStopPreventsRevive(t *testing.T) {
	m, b, mock, calls := activeDropHarness(t, 10*time.Second, 20*time.Millisecond)
	m.Stop()

	m.Start()
	m.Restart()
	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt32(calls); got != 0 {
		t.Fatalf("stopped manager revived: factory called %d times", got)
	}
	if m.stateLocked() != UpgradeIdle {
		t.Fatalf("state must stay Idle after Stop, got %v", m.stateLocked())
	}

	_ = mock
	_ = b
}

func TestUpgradeWaitReadyStoppedDoesNotAttachTransport(t *testing.T) {
	mock := &mockTransport{}
	sess := NewSession("wss://test.local")
	b := NewBroker(sess)
	t.Cleanup(func() { b.Stop() })
	ready := make(chan struct{})
	var factoryCalls int32
	factory := func() (Transport, <-chan struct{}, func(func(SignalMessage), <-chan SignalMessage) error, func(), error) {
		atomic.AddInt32(&factoryCalls, 1)
		startNeg := func(func(SignalMessage), <-chan SignalMessage) error { return nil }
		return mock, ready, startNeg, func() {}, nil
	}
	m := NewUpgradeManager(b, factory, UpgradeConfig{
		Enabled:    true,
		ICETimeout: 10 * time.Second,
		RetryDelay: time.Hour,
	})
	t.Cleanup(func() { m.Stop() })

	go m.runUpgrade(make(chan SignalMessage, 1))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&factoryCalls) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&factoryCalls) != 1 {
		t.Fatal("factory never called - runUpgrade did not reach negotiation")
	}
	time.Sleep(100 * time.Millisecond)

	m.stopped.Store(true)
	close(ready)

	time.Sleep(150 * time.Millisecond)
	if b.HasP2PTransport() {
		t.Fatal("transport attached to a stopped broker after DataChannel ready")
	}
	if m.stateLocked() == UpgradeActive {
		t.Fatal("bogus UpgradeActive notify fired after Stop")
	}
}
