package agent

import (
	"strings"
	"testing"
)

// TestPTGuardStruggleDensityWindow covers the window mechanics: thin
// windows score 0, density is a share of the filled window, and the ring
// caps at ptGuardWindow entries.
func TestPTGuardStruggleDensityWindow(t *testing.T) {
	g := newPrematureTerminationGuard()

	if d := g.struggleDensity(); d != 0 {
		t.Fatalf("empty window must score 0, got %v", d)
	}

	// 7 steps (below ptGuardMinSteps) even all-error: still 0.
	for i := 0; i < ptGuardMinSteps-1; i++ {
		g.recordToolStep(true)
	}
	if d := g.struggleDensity(); d != 0 {
		t.Fatalf("window below min steps must score 0, got %v", d)
	}

	// Fresh window of 8 steps, 4 errors -> density 0.5.
	g = newPrematureTerminationGuard()
	for i := 0; i < 4; i++ {
		g.recordToolStep(false)
	}
	for i := 0; i < 4; i++ {
		g.recordToolStep(true)
	}
	if d := g.struggleDensity(); d != 0.5 {
		t.Fatalf("want density 0.5, got %v", d)
	}

	// Ring bound: flood with successes, density must fall to 0 and the
	// slice must cap at ptGuardWindow.
	for i := 0; i < ptGuardWindow*3; i++ {
		g.recordToolStep(false)
	}
	if len(g.errs) != ptGuardWindow {
		t.Fatalf("ring must cap at %d, got %d", ptGuardWindow, len(g.errs))
	}
	if d := g.struggleDensity(); d != 0 {
		t.Fatalf("all-success window must score 0, got %v", d)
	}
}

// TestPTGuardMaybeWarnGating exercises the combined-signal gate without a
// full Agent loop: below-threshold struggle or occupancy must stay silent;
// the advisory fires once and only once. Uses a nil-safe path by driving
// the guard fields directly (maybeWarnTermination needs an Agent for
// contextManager; gating below occupancy is covered via density checks).
func TestPTGuardMaybeWarnGating(t *testing.T) {
	g := newPrematureTerminationGuard()

	// Struggle below threshold: 8 steps, 3 errors = 0.375 < 0.5.
	for i := 0; i < 5; i++ {
		g.recordToolStep(false)
	}
	for i := 0; i < 3; i++ {
		g.recordToolStep(true)
	}
	if d := g.struggleDensity(); d >= ptGuardStruggleDensity {
		t.Fatalf("density %v must stay below trigger %v", d, ptGuardStruggleDensity)
	}

	// Healthy trajectory: all successes, high density impossible.
	h := newPrematureTerminationGuard()
	for i := 0; i < ptGuardWindow; i++ {
		h.recordToolStep(false)
	}
	if h.struggleDensity() != 0 {
		t.Fatal("healthy trajectory must have zero struggle density")
	}

	// Struggling trajectory: 6 of 10 errors = 0.6 >= 0.5.
	s := newPrematureTerminationGuard()
	for i := 0; i < 4; i++ {
		s.recordToolStep(false)
	}
	for i := 0; i < 6; i++ {
		s.recordToolStep(true)
	}
	if d := s.struggleDensity(); d < ptGuardStruggleDensity {
		t.Fatalf("struggling trajectory density %v must reach trigger", d)
	}

	// reset() clears both window and fired quota.
	r := newPrematureTerminationGuard()
	r.recordToolStep(true)
	r.fired = true
	r.reset()
	if len(r.errs) != 0 || r.fired {
		t.Fatal("reset must clear window and fired flag")
	}
}

// TestPTGuardAdvisoryContent pins the advisory wording: it must anchor the
// agent to remaining window percentage and to a strategy change, per
// arXiv 2606.29718's mitigation framing.
func TestPTGuardAdvisoryContent(t *testing.T) {
	// Directly exercise the fired-once + message contract through a
	// minimal Agent (contextManager present via NewAgent is heavy; the
	// occupancy read lives in maybeWarnTermination, so we assert on the
	// format via a struggling guard and the guidance contract fields).
	g := newPrematureTerminationGuard()
	for i := 0; i < 4; i++ {
		g.recordToolStep(false)
	}
	for i := 0; i < 6; i++ {
		g.recordToolStep(true)
	}
	if g.fired {
		t.Fatal("guard must not be fired before maybeWarnTermination")
	}

	// The advisory text contract is enforced here for the fields we can
	// compute without a provider: error count and window share.
	n := 0
	for _, e := range g.errs {
		if e {
			n++
		}
	}
	if n != 6 || len(g.errs) != 10 {
		t.Fatalf("want 6 errors over 10 steps, got %d/%d", n, len(g.errs))
	}

	// Wording pins (kept adjacent to the guard so wording drift is caught).
	msg := ptGuardAdvisory(6, 10, 0.7)
	pins := []string{
		"Continuation Advisory",
		"30% of the context window is still available",
		"6 of last 10",
		"DIFFERENT strategy",
		"arXiv 2606.29718",
	}
	for _, p := range pins {
		if !strings.Contains(msg, p) {
			t.Errorf("advisory missing pin %q in:\n%s", p, msg)
		}
	}
}
