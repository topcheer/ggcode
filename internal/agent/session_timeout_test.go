package agent

import (
	"testing"
	"time"
)

func TestSessionTimeoutDisabled(t *testing.T) {
	s := newSessionTimeoutState(0)
	s.start(false)

	// Disabled timeout: check() returns empty, shouldStop() returns false
	if msg := s.check(); msg != "" {
		t.Fatalf("expected empty message for disabled timeout, got %q", msg)
	}
	if s.shouldStop() {
		t.Fatal("expected shouldStop=false for disabled timeout")
	}
}

func TestSessionTimeoutNotYetExpired(t *testing.T) {
	s := newSessionTimeoutState(10 * time.Minute)
	s.start(false)

	// Immediately after start, timeout hasn't expired
	if msg := s.check(); msg != "" {
		t.Fatalf("expected empty message before timeout, got %q", msg)
	}
	if s.shouldStop() {
		t.Fatal("expected shouldStop=false before timeout")
	}
}

func TestSessionTimeoutExpired(t *testing.T) {
	s := &sessionTimeoutState{
		timeout: 1 * time.Nanosecond,
	}
	s.start(false)

	// Wait a tiny bit so elapsed > timeout
	time.Sleep(2 * time.Millisecond)

	if !s.shouldStop() {
		t.Fatal("expected shouldStop=true after timeout")
	}
	msg := s.check()
	if msg == "" {
		t.Fatal("expected non-empty message after timeout")
	}
}

func TestSessionTimeoutWarnings(t *testing.T) {
	s := &sessionTimeoutState{
		timeout: 100 * time.Millisecond,
	}
	s.start(false)

	// Wait past 80% of timeout
	time.Sleep(85 * time.Millisecond)
	msg := s.check()
	if msg == "" {
		t.Fatal("expected warning message")
	}
	if !contains(msg, "80%") {
		t.Fatalf("expected 80%% in message, got %q", msg)
	}
}

func TestSessionTimeoutAutopilotDefault(t *testing.T) {
	s := newSessionTimeoutState(0)
	// In autopilot mode, a default timeout should be applied.
	// #4514: the resolved value lives on runTimeout (per-run) - the old
	// write-back into s.timeout leaked the 600m default into every later
	// interactive run on the same Agent.
	s.start(true)
	if s.runTimeout != defaultAutopilotSessionTimeout {
		t.Fatalf("expected default autopilot timeout %v, got %v", defaultAutopilotSessionTimeout, s.runTimeout)
	}
	if s.timeout != 0 {
		t.Fatalf("configured timeout must stay untouched, got %v", s.timeout)
	}
}

func TestSessionTimeoutNoAutopilotDefault(t *testing.T) {
	s := newSessionTimeoutState(0)
	// Not autopilot: timeout stays disabled
	s.start(false)
	if s.timeout != 0 {
		t.Fatalf("expected timeout=0 for non-autopilot, got %v", s.timeout)
	}
}

func TestEffectiveSessionTimeout(t *testing.T) {
	// Configured timeout takes priority
	got := EffectiveSessionTimeout(5*time.Minute, true)
	if got != 5*time.Minute {
		t.Fatalf("expected 5m, got %v", got)
	}

	// No config, autopilot: default
	got = EffectiveSessionTimeout(0, true)
	if got != defaultAutopilotSessionTimeout {
		t.Fatalf("expected default autopilot timeout, got %v", got)
	}

	// No config, not autopilot: disabled
	got = EffectiveSessionTimeout(0, false)
	if got != 0 {
		t.Fatalf("expected 0, got %v", got)
	}
}

func TestSessionTimeoutWarningOnce(t *testing.T) {
	s := &sessionTimeoutState{
		timeout: 100 * time.Millisecond,
	}
	s.start(false)

	// First check after 80ms should trigger warning
	time.Sleep(85 * time.Millisecond)
	msg1 := s.check()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second check should NOT trigger again (already warned)
	msg2 := s.check()
	if msg2 != "" {
		t.Fatalf("expected empty second warning (already fired), got %q", msg2)
	}
}
