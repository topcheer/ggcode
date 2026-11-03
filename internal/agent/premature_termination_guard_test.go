package agent

import (
	"strings"
	"testing"
)

func TestPTGuardStruggleDensityWindow(t *testing.T) {
	g := newPrematureTerminationGuard()

	if d := g.struggleDensity(); d != 0 {
		t.Fatalf("empty window must score 0, got %v", d)
	}

	for i := 0; i < ptGuardMinSteps-1; i++ {
		g.recordToolStep(true)
	}
	if d := g.struggleDensity(); d != 0 {
		t.Fatalf("window below min steps must score 0, got %v", d)
	}

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

func TestPTGuardMaybeWarnGating(t *testing.T) {
	g := newPrematureTerminationGuard()

	for i := 0; i < 5; i++ {
		g.recordToolStep(false)
	}
	for i := 0; i < 3; i++ {
		g.recordToolStep(true)
	}
	if d := g.struggleDensity(); d >= ptGuardStruggleDensity {
		t.Fatalf("density %v must stay below trigger %v", d, ptGuardStruggleDensity)
	}

	h := newPrematureTerminationGuard()
	for i := 0; i < ptGuardWindow; i++ {
		h.recordToolStep(false)
	}
	if h.struggleDensity() != 0 {
		t.Fatal("healthy trajectory must have zero struggle density")
	}

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

	r := newPrematureTerminationGuard()
	r.recordToolStep(true)
	r.fired = true
	r.reset()
	if len(r.errs) != 0 || r.fired {
		t.Fatal("reset must clear window and fired flag")
	}
}

func TestPTGuardAdvisoryContent(t *testing.T) {

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

	n := 0
	for _, e := range g.errs {
		if e {
			n++
		}
	}
	if n != 6 || len(g.errs) != 10 {
		t.Fatalf("want 6 errors over 10 steps, got %d/%d", n, len(g.errs))
	}

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
