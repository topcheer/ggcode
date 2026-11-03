package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	ptGuardWindow          = 12
	ptGuardMinSteps        = 8
	ptGuardStruggleDensity = 0.5
	ptGuardOccupancy       = 0.6
)

type prematureTerminationGuard struct {
	errs  []bool
	fired bool
}

func newPrematureTerminationGuard() *prematureTerminationGuard {
	return &prematureTerminationGuard{}
}

func (g *prematureTerminationGuard) reset() {
	g.errs = nil
	g.fired = false
}

func (g *prematureTerminationGuard) recordToolStep(isError bool) {
	g.errs = append(g.errs, isError)
	if len(g.errs) > ptGuardWindow {
		g.errs = g.errs[len(g.errs)-ptGuardWindow:]
	}
}

func (g *prematureTerminationGuard) struggleDensity() float64 {
	if len(g.errs) < ptGuardMinSteps {
		return 0
	}
	n := 0
	for _, e := range g.errs {
		if e {
			n++
		}
	}
	return float64(n) / float64(len(g.errs))
}

func ptGuardAdvisory(errSteps, steps int, occupancy float64) string {
	remainingPct := int((1 - occupancy) * 100)
	return fmt.Sprintf(
		"[Continuation Advisory] Recent steps are failing repeatedly (%d of last %d) while context usage is high (%d%% used). "+
			"%d%% of the context window is still available - do not wrap up or give up yet. "+
			"Instead of surrendering to the errors: (1) re-read the exact error text of the LAST failure, "+
			"(2) name the assumption that failure disproved, (3) pick a DIFFERENT strategy (narrower scope, "+
			"smaller repro, different tool) rather than retrying the same shape. "+
			"Premature surrender under long context is a documented model failure mode (arXiv 2606.29718).",
		errSteps, steps, int(occupancy*100), remainingPct)
}

func (a *Agent) maybeWarnTermination() string {
	g := a.termGuard
	if g == nil || g.fired {
		return ""
	}
	if len(g.errs) < ptGuardMinSteps {
		return ""
	}
	density := g.struggleDensity()
	if density < ptGuardStruggleDensity {
		return ""
	}
	occupancy := a.contextManager.UsageRatio()
	if occupancy < ptGuardOccupancy {
		return ""
	}

	g.fired = true
	errSteps := 0
	for _, e := range g.errs {
		if e {
			errSteps++
		}
	}
	debug.Log("term-guard",
		"premature termination risk: struggle density=%.2f over %d steps, context occupancy=%.0f%%",
		density, len(g.errs), occupancy*100)
	return ptGuardAdvisory(errSteps, len(g.errs), occupancy)
}
