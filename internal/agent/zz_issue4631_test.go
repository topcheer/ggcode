package agent

import (
	"strings"
	"testing"
	"time"
)

// #4631: with the default configured timeout=0 + autopilot, start() resolves
// runTimeout to the 600m default while s.timeout stays 0 - shouldStop() used
// to judge s.timeout and was therefore permanently false, so check()'s stop
// message fired but the loop never broke. shouldStop must judge runTimeout.
func TestIssue4631_ShouldStopUsesRunTimeout(t *testing.T) {
	s := newSessionTimeoutState(0) // default config: 0 = disabled interactively
	s.start(true)                  // autopilot: runTimeout resolves to 600m
	if s.runTimeout != defaultAutopilotSessionTimeout {
		t.Fatalf("runTimeout=%s, want the autopilot default", s.runTimeout)
	}
	if s.shouldStop() {
		t.Fatal("fresh run must not stop immediately")
	}

	// Simulate elapsed time past the limit by back-dating the start.
	s.startTime = time.Now().Add(-(defaultAutopilotSessionTimeout + time.Minute))
	if !s.shouldStop() {
		t.Fatal("past the autopilot wall-clock limit, shouldStop must be true (#4631)")
	}
}

// #4631: the 80% warning text must report the effective per-run timeout
// (runTimeout), not the configured value which reads 0s in the
// default+autopilot scenario.
func TestIssue4631_EightyPercentWarningUsesRunTimeout(t *testing.T) {
	s := newSessionTimeoutState(0)
	s.start(true)
	// Back-date to 85% elapsed: fires the 80% branch (95% not yet).
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
