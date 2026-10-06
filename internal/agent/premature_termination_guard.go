package agent

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

// Premature Termination Guard (r26, research round sa-57 gap).
//
// Research basis: arXiv 2606.29718 "Diagnosing and Mitigating Context Rot
// in Long-horizon Search" (GAIR-NLP/SJTU 2026; four flagship models, three
// benchmarks, five repeats). Two findings:
//
//  1. Premature termination: under extensive context, models give up or
//     submit uncertain incorrect answers LONG BEFORE the context window is
//     exhausted. The premature-termination rate is positively correlated
//     with context length even when query difficulty is held fixed.
//  2. Struggle foreshadows it: trajectories that end in give-up or
//     uncertain-incorrect states have a significantly higher share of
//     "struggle" steps (repeated failed attempts, no progress) - the
//     abandonment is semantically observable BEFORE it happens.
//
// ggcode already detects surrender AFTER the fact (giveup_revert fires on
// give-up wording) and single-signal loops (futile_cycle: duplicate reads
// alone; iter_pressure: iteration budget x verify-rate). None combine
// struggle density WITH context occupancy to intervene BEFORE the agent
// drafts its give-up answer - which is the only phase where "the window is
// still 40% empty, enumerate what you haven't tried" can change the outcome.
//
// Deterministic, zero-LLM-cost heuristic:
//   - struggle proxy: rolling window of tool-step error flags; density =
//     errored steps / window size (failed attempts dominate recent work)
//   - occupancy: context.Manager.UsageRatio() (the paper's core variable)
//   - both over threshold -> inject ONCE per run: remaining-window % plus
//     a structure for continuing exploration instead of surrendering.

const (
	// ptGuardWindow is the rolling struggle window (tool steps).
	ptGuardWindow = 12
	// ptGuardMinSteps is the minimum filled window before density means
	// anything (avoids 1/2 = 0.5 firing on noise).
	ptGuardMinSteps = 8
	// ptGuardStruggleDensity: errored-step share at or above which the
	// recent trajectory counts as struggling (paper: struggle-heavy
	// trajectories terminate prematurely).
	ptGuardStruggleDensity = 0.5
	// ptGuardOccupancy: context usage at or above which premature
	// termination risk rises (paper: rate grows with context length;
	// 60% leaves 40% of the window - still substantial exploration room).
	ptGuardOccupancy = 0.6
)

// prematureTerminationGuard tracks recent tool-step outcomes and fires a
// continue-exploration advisory when struggle density and context
// occupancy are simultaneously high.
type prematureTerminationGuard struct {
	errs  []bool // rolling window of per-step error flags
	fired bool
}

func newPrematureTerminationGuard() *prematureTerminationGuard {
	return &prematureTerminationGuard{}
}

func (g *prematureTerminationGuard) reset() {
	g.errs = nil
	g.fired = false
}

// recordToolStep files one executed tool step's error flag. Called from
// the tool-result handling loop where result.IsError is known.
func (g *prematureTerminationGuard) recordToolStep(isError bool) {
	g.errs = append(g.errs, isError)
	if len(g.errs) > ptGuardWindow {
		g.errs = g.errs[len(g.errs)-ptGuardWindow:]
	}
}

// struggleDensity returns the errored-step share of the current window
// (0 when the window is too thin to judge).
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

// ptGuardAdvisory renders the continue-exploration message. Package-level
// so the wording contract is directly testable.
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

// maybeWarnTermination checks the combined signal and returns guidance
// when the run is at risk of a premature give-up. occupancy comes from
// context.Manager.UsageRatio(); fired-once per run.
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
