package agent

import (
	"strings"
	"testing"
)

// TestForesightScoreboard pins #uncertainty-loop P2: session-level hits
// accumulate across turn resets and a scoreboard fires every 10 scored
// predictions with calibrated advice.
func TestForesightScoreboard(t *testing.T) {
	s := newForesightCalibrateState()
	// Drive checkCalibration-like accounting directly through the state:
	// simulate 12 confirmed predictions (hits) by manipulating the session
	// counters via the same code path used on success.
	s.sessionPreds = 9
	s.sessionHits = 9
	got := foresightScoreboardLine(s, true)
	if got == "" || !strings.Contains(got, "calibration scoreboard: 10 scored predictions") {
		t.Fatalf("10th hit must fire scoreboard, got %q", got)
	}
	if !strings.Contains(got, "well-calibrated") {
		t.Fatalf("10/10 must carry well-calibrated advice, got %q", got)
	}
	// An 11th confirmed prediction stays silent.
	if got := foresightScoreboardLine(s, true); got != "" {
		t.Fatalf("non-multiple predictions stay silent, got %q", got)
	}
	// Poor calibration flips the advice.
	s2 := newForesightCalibrateState()
	s2.sessionPreds = 9
	s2.sessionHits = 2
	got2 := foresightScoreboardLine(s2, true)
	if !strings.Contains(got2, "Calibration is poor") {
		t.Fatalf("low hit rate must carry poor-calibration advice, got %q", got2)
	}
	// Turn-scoped reset must NOT clear session counters.
	s2.reset()
	if s2.sessionPreds != 10 || s2.sessionHits != 3 {
		t.Fatalf("session counters must survive reset, got preds=%d hits=%d", s2.sessionPreds, s2.sessionHits)
	}
}
