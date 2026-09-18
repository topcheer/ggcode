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

// Blind-spot errors must NOT auto-resubmit. The earlier auto-retry design
// had three compounding failure modes: the counter was written into a
// value-receiver model copy so the parent never saw it (every retry showed
// 1/5 and the loop was unbounded); submitText re-persisted the user turn
// (duplicate session entries on agent-side failures); and image
// attachments were dropped (retry replayed text only). The replacement is
// diagnosis-only: enable file logging and point the user at /retry.

// TestBlindSpotRetryNeverSchedulesAutoResubmit pins the removal: no tick
// command is ever scheduled, regardless of counter state.
func TestBlindSpotRetryNeverSchedulesAutoResubmit(t *testing.T) {
	m := blindSpotSetup(t)
	m.blindSpotRetries = 0

	cmd := m.maybeBlindSpotRetry(errors.New("totally unknown failure mode"))
	if cmd != nil {
		t.Fatalf("no auto-retry may be scheduled (first failure)")
	}
	if m.blindSpotRetries != 0 {
		t.Fatalf("counter must not advance, got %d", m.blindSpotRetries)
	}

	// Even with a burnt budget, behavior is identical (no special branch).
	m.blindSpotRetries = 99
	cmd = m.maybeBlindSpotRetry(errors.New("gateway said 200 but body was html"))
	if cmd != nil {
		t.Fatalf("no auto-retry may be scheduled (burnt budget)")
	}
	if m.blindSpotRetries != 99 {
		t.Fatalf("counter must not be touched, got %d", m.blindSpotRetries)
	}
}

// TestBlindSpotNoticePointsAtManualRetry pins the user-facing path: the
// notice enables debug logging and offers /retry instead of auto-resending.
func TestBlindSpotNoticePointsAtManualRetry(t *testing.T) {
	m := blindSpotSetup(t)

	m.maybeBlindSpotRetry(errors.New("totally unknown failure mode"))

	out := renderedOutput(m)
	if !strings.Contains(out, "Unrecognized error detected") {
		t.Fatalf("expected blind-spot notice, got %q", out)
	}
	if !strings.Contains(out, "/retry") {
		t.Fatalf("expected manual /retry hint, got %q", out)
	}
	if strings.Contains(out, "Auto-retrying") || strings.Contains(out, "Auto-retry limit") {
		t.Fatalf("no auto-retry wording may remain, got %q", out)
	}
}

// TestBlindSpotNoRetryHintWithoutSubmission pins the edge case: with no
// previous submission there is nothing /retry could resend, so the hint
// is omitted.
func TestBlindSpotNoRetryHintWithoutSubmission(t *testing.T) {
	m := blindSpotSetup(t)
	m.lastUserSubmission = ""

	m.maybeBlindSpotRetry(errors.New("unknown"))

	out := renderedOutput(m)
	if !strings.Contains(out, "Unrecognized error detected") {
		t.Fatalf("expected blind-spot notice, got %q", out)
	}
	if strings.Contains(out, "/retry") {
		t.Fatalf("no /retry hint without a submission, got %q", out)
	}
}

// TestBlindSpotResetOnSuccess stays intact: clearing the (now inert)
// counter on success keeps the field consistent for future features.
func TestBlindSpotResetOnSuccess(t *testing.T) {
	m := blindSpotSetup(t)
	m.blindSpotRetries = 3
	m.resetBlindSpotRetry()
	if m.blindSpotRetries != 0 {
		t.Fatalf("counter must reset, got %d", m.blindSpotRetries)
	}
}
