package tui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

func blindSpotSetup(t *testing.T) *Model {
	t.Cleanup(func() {
		debug.Close()
		os.Unsetenv("GGCODE_DEBUG")
	})
	m := newTestModel()
	m.loading = true
	m.activeAgentRunID = 9
	m.lastUserSubmission = "hello"
	return &m
}

// TestBlindSpotRetryBudgetStopsAtMax pins the 5-attempt cap: the sixth
// blind-spot failure must not schedule another retry.
func TestBlindSpotRetryBudgetStopsAtMax(t *testing.T) {
	m := blindSpotSetup(t)
	m.blindSpotRetries = blindSpotMaxRetries

	cmd := m.maybeBlindSpotRetry(errors.New("totally unknown failure mode"))

	if cmd != nil {
		t.Fatalf("retry must not be scheduled once the budget is spent")
	}
	out := renderedOutput(m)
	if !strings.Contains(out, "Auto-retry limit reached") {
		t.Fatalf("expected exhaustion notice, got %q", out)
	}
	if m.blindSpotRetries != blindSpotMaxRetries {
		t.Fatalf("counter must not exceed max, got %d", m.blindSpotRetries)
	}
}

// TestBlindSpotRetryCountdownAndSchedule pins the happy path: counter
// advances, notice rendered, tick cmd scheduled.
func TestBlindSpotRetryCountdownAndSchedule(t *testing.T) {
	m := blindSpotSetup(t)

	cmd := m.maybeBlindSpotRetry(errors.New("gateway said 200 but body was html"))

	if cmd == nil {
		t.Fatalf("expected retry cmd")
	}
	if m.blindSpotRetries != 1 {
		t.Fatalf("expected counter=1, got %d", m.blindSpotRetries)
	}
	out := renderedOutput(m)
	if !strings.Contains(out, "Auto-retrying in 5 seconds (1/5)") {
		t.Fatalf("expected countdown notice, got %q", out)
	}
	if !strings.Contains(out, "ggcode-debug") {
		t.Fatalf("expected log path in notice, got %q", out)
	}
}

// TestBlindSpotNoRetryWithQueue pins the conservative rule: when queued
// submissions took over, no auto-retry: races the queue.
func TestBlindSpotNoRetryWithQueue(t *testing.T) {
	m := blindSpotSetup(t)
	m.lastUserSubmission = ""
	cmd := m.maybeBlindSpotRetry(errors.New("unknown"))
	if cmd != nil {
		t.Fatalf("no retry without a submission to repeat")
	}
}

// TestBlindSpotRetryDroppedWhenLoading pins the timer handler: if the user
// started a new run during the 5s window, the retry is discarded.
func TestBlindSpotRetryDroppedWhenLoading(t *testing.T) {
	m := blindSpotSetup(t)
	m.loading = true
	cmd := m.handleBlindSpotRetryMsg(blindSpotRetryMsg{Text: "hello"})
	if cmd != nil {
		t.Fatalf("retry must be dropped while a run is active")
	}
}

// TestBlindSpotResetOnSuccess pins budget restoration: a successful run
// gives the next failure a full 5 attempts again.
func TestBlindSpotResetOnSuccess(t *testing.T) {
	m := blindSpotSetup(t)
	m.blindSpotRetries = 4
	m.resetBlindSpotRetry()
	if m.blindSpotRetries != 0 {
		t.Fatalf("expected reset to 0, got %d", m.blindSpotRetries)
	}
}
