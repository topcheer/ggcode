package agent

import (
	"strings"
	"testing"
)

// TestConfidenceTrajectorySustainedDegradation (r75): the micro-stability
// feature — confSustainedTurns consecutive degraded turns — fires exactly
// once per degradation episode, stays quiet within it, and re-arms after a
// recovery turn (arXiv:2601.15778 episode semantics).
func TestConfidenceTrajectorySustainedDegradation(t *testing.T) {
	tr := newConfidenceTrajectory()
	// Healthy baseline: never fires.
	for _, c := range []float64{-0.2, -0.4} {
		if _, fire := tr.observe(c); fire {
			t.Fatalf("healthy turn %.2f must not escalate", c)
		}
	}
	// Sustained degradation: healthy samples still occupy the window, so
	// the 3-consecutive-turn streak needs three degraded turns after them.
	if _, fire := tr.observe(-1.9); fire {
		t.Fatal("degraded turn with healthy samples in window must not escalate")
	}
	if _, fire := tr.observe(-2.0); fire {
		t.Fatal("streak not yet sustained (healthy sample still in window)")
	}
	notice, fire := tr.observe(-2.1)
	if !fire || !strings.Contains(notice, "[confidence]") {
		t.Fatalf("expected escalation once window is fully degraded, fire=%v notice=%q", fire, notice)
	}
	// Episode active: further degraded turns stay quiet (rate-limited).
	if _, fire := tr.observe(-2.2); fire {
		t.Fatal("must not re-escalate within the same episode")
	}
	// Recovery re-arms the tracker.
	if _, fire := tr.observe(-0.1); fire {
		t.Fatal("recovery turn must not escalate")
	}
	// A second episode can escalate again after recovery.
	tr.observe(-1.8)
	tr.observe(-1.9)
	if _, fire = tr.observe(-2.0); !fire {
		t.Fatal("expected second episode escalation after recovery")
	}
}

// TestConfidenceTrajectoryMacroTrend (r75): the macro-dynamics feature —
// recent window half meaningfully below the older half — fires even when no
// individual turn crosses the sustained threshold.
func TestConfidenceTrajectoryMacroTrend(t *testing.T) {
	tr := newConfidenceTrajectory()
	// Older half mild (-0.2 avg), recent half clearly worse (~-1.13 avg):
	// drop >= confTrendDrop with recent mean < 0, but no 3-turn streak
	// below confSustainedLow — isolates the trend feature.
	seq := []float64{-0.1, -0.2, -0.3, -1.1, -1.2, -1.1}
	for i, c := range seq[:len(seq)-1] {
		if _, fire := tr.observe(c); fire {
			t.Fatalf("turn %d (%.2f) escalated before trend is observable", i, c)
		}
	}
	notice, fire := tr.observe(seq[len(seq)-1])
	if !fire || !strings.Contains(notice, "[confidence]") {
		t.Fatalf("expected trend-based escalation on 6th turn, fire=%v notice=%q", fire, notice)
	}
}

// TestConfidenceTrajectoryNoFalsePositive (r75): mildly variable turns never
// escalate, and the window cap drops old history so stale degradation does
// not trigger a spurious trend later.
func TestConfidenceTrajectoryNoFalsePositive(t *testing.T) {
	tr := newConfidenceTrajectory()
	for i := 0; i < 40; i++ {
		c := -0.5 + float64(i%3)*0.15 // oscillates between -0.5 and -0.2
		if _, fire := tr.observe(c); fire {
			t.Fatalf("healthy oscillating turn %d (%.2f) must not escalate", i, c)
		}
	}
	if len(tr.window) > confWindow {
		t.Fatalf("window cap exceeded: %d > %d", len(tr.window), confWindow)
	}
	// Single hard-low turn: point-check territory (caller), tracker silent.
	if _, fire := tr.observe(-3.0); fire {
		t.Fatal("single low turn must not escalate at trajectory level")
	}
}
