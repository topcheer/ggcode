package agent

import (
	"strings"
	"testing"
	"time"
)

func TestIssue4631_ShouldStopUsesRunTimeout(t *testing.T) {
	s := newSessionTimeoutState(0)
	s.start(true)
	if s.runTimeout != defaultAutopilotSessionTimeout {
		t.Fatalf("runTimeout=%s, want the autopilot default", s.runTimeout)
	}
	if s.shouldStop() {
		t.Fatal("fresh run must not stop immediately")
	}

	s.startTime = time.Now().Add(-(defaultAutopilotSessionTimeout + time.Minute))
	if !s.shouldStop() {
		t.Fatal("past the autopilot wall-clock limit, shouldStop must be true (#4631)")
	}
}

func TestIssue4631_EightyPercentWarningUsesRunTimeout(t *testing.T) {
	s := newSessionTimeoutState(0)
	s.start(true)

	s.startTime = time.Now().Add(-time.Duration(0.85 * float64(defaultAutopilotSessionTimeout)))
	msg := s.check()
	if !strings.Contains(msg, "80%") {
		t.Fatalf("expected the 80%% warning, got %q", msg)
	}
	if strings.Contains(msg, "of the 0s wall-clock") {
		t.Fatalf("80%% warning must report the effective timeout, not the configured 0s (#4631): %q", msg)
	}
	if !strings.Contains(msg, defaultAutopilotSessionTimeout.Round(time.Second).String()) {
		t.Fatalf("80%% warning must report the runTimeout value (#4631): %q", msg)
	}
}
