package im

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

// Pin tests for the WhatsApp event-handler dispatch: they lock the observable
// behavior of each event kind BEFORE the eventHandler seam refactor and must
// stay green after it. #974 gate semantics under test: Disconnected signals a
// retryable error, LoggedOut signals the terminal errWhatsAppLoggedOut, and a
// buffered retryable signal must upgrade to the terminal one.

func newEventHandlerTestAdapter() *whatsappAdapter {
	return &whatsappAdapter{name: "t", sessionDone: make(chan error, 1)}
}

func waitSessionSignal(t *testing.T, a *whatsappAdapter) error {
	t.Helper()
	select {
	case err := <-a.sessionDone:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session signal")
		return nil
	}
}

func assertNoSessionSignal(t *testing.T, a *whatsappAdapter) {
	t.Helper()
	select {
	case err := <-a.sessionDone:
		t.Fatalf("unexpected session signal: %v", err)
	default:
	}
}

// TestWhatsAppEventHandlerConnectedState: Connected sets the connected flag,
// clears the pairing QR, and must NOT signal sessionDone (the session is
// alive). manager is nil so publishState is a no-op in tests.
func TestWhatsAppEventHandlerConnectedState(t *testing.T) {
	a := newEventHandlerTestAdapter()
	a.lastQR = "QRDATA"
	h := a.eventHandler()

	h(&events.Connected{})

	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.connected {
		t.Fatal("Connected event must set connected=true")
	}
	if a.lastQR != "" {
		t.Fatalf("Connected event must clear lastQR, got %q", a.lastQR)
	}
	assertNoSessionSignal(t, a)
}

// TestWhatsAppEventHandlerDisconnectedRetryableSignal: Disconnected clears the
// connected flag and signals a retryable (non-terminal) session error.
func TestWhatsAppEventHandlerDisconnectedRetryableSignal(t *testing.T) {
	a := newEventHandlerTestAdapter()
	a.mu.Lock()
	a.connected = true
	a.mu.Unlock()
	h := a.eventHandler()

	h(&events.Disconnected{})

	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if connected {
		t.Fatal("Disconnected event must set connected=false")
	}
	err := waitSessionSignal(t, a)
	if waTerminal(err) {
		t.Fatalf("Disconnected signal must be retryable, got terminal %v", err)
	}
	if want := "whatsapp disconnected"; err == nil || err.Error() != want {
		t.Fatalf("want %q signal, got %v", want, err)
	}
}

// TestWhatsAppEventHandlerLoggedOutTerminalSignal: LoggedOut clears the client
// (markLoggedOut), resets connected, and signals the terminal
// errWhatsAppLoggedOut which drives store DB cleanup (#974).
func TestWhatsAppEventHandlerLoggedOutTerminalSignal(t *testing.T) {
	a := newEventHandlerTestAdapter()
	h := a.eventHandler()

	h(&events.LoggedOut{})

	a.mu.RLock()
	connected := a.connected
	client := a.client
	a.mu.RUnlock()
	if connected {
		t.Fatal("LoggedOut event must set connected=false")
	}
	if client != nil {
		t.Fatal("LoggedOut event must clear the client")
	}
	err := waitSessionSignal(t, a)
	if !errors.Is(err, errWhatsAppLoggedOut) {
		t.Fatalf("LoggedOut signal must be terminal errWhatsAppLoggedOut, got %v", err)
	}
}

// TestWhatsAppEventHandlerGateOrderDisconnectedThenLoggedOut: the #974 upgrade
// path through the real event dispatch - a Disconnected that fills the done
// buffer, followed by LoggedOut, must leave the terminal error as THE signal.
func TestWhatsAppEventHandlerGateOrderDisconnectedThenLoggedOut(t *testing.T) {
	a := newEventHandlerTestAdapter()
	h := a.eventHandler()

	h(&events.Disconnected{})
	h(&events.LoggedOut{})

	err := waitSessionSignal(t, a)
	if !errors.Is(err, errWhatsAppLoggedOut) {
		t.Fatalf("buffered Disconnected signal must upgrade to terminal LoggedOut, got %v", err)
	}
}

// TestWhatsAppEventHandlerInfoEventsNoStateChange: log-only informational
// events must not touch adapter state or signal sessionDone.
func TestWhatsAppEventHandlerInfoEventsNoStateChange(t *testing.T) {
	infoEvents := []interface{}{
		&events.HistorySync{},
		&events.OfflineSyncPreview{},
		&events.OfflineSyncCompleted{},
		&events.JoinedGroup{},
		&events.GroupInfo{},
		&events.PairSuccess{},
		&events.PairError{},
	}
	for _, evt := range infoEvents {
		a := newEventHandlerTestAdapter()
		a.eventHandler()(evt)

		a.mu.RLock()
		connected := a.connected
		a.mu.RUnlock()
		if connected {
			t.Fatalf("%T must not set connected=true", evt)
		}
		assertNoSessionSignal(t, a)
	}
}

// TestWhatsAppEventHandlerUnknownEventNoPanic: unknown event types fall into
// the default branch (logged, ignored) without touching state.
func TestWhatsAppEventHandlerUnknownEventNoPanic(t *testing.T) {
	a := newEventHandlerTestAdapter()
	a.eventHandler()(struct{}{})

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.connected {
		t.Fatal("unknown event must not set connected=true")
	}
	assertNoSessionSignal(t, a)
}
