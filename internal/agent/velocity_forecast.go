package agent

// Iteration Velocity Forecaster -- Predictive Productivity Budget Analysis
//
// Research basis: "Trajectory-Aware Agent Steering" (TAAS, arXiv:2607.04291, Jul 2026)
// Key finding: agents fail to self-regulate their pace. When the productive-to-total
// iteration ratio is low early in a run, the agent is overwhelmingly likely to run
// out of iterations before completing the task. TAAS showed that injecting a
// data-driven forecast at ~40% of iteration budget -- NOT a generic "are you on track?"
// question but actual productivity metrics -- shifts agent strategy 2x more effectively
// than reactive stall detection.
//
// Gap in existing ggcode systems:
//   - budget_guard.go: tracks TOKEN COST escalation (spending too much per step),
//     not whether the agent is making PROGRESS fast enough to finish.
//   - overseer.go: detects absolute stalls (N iterations without productive action)
//     -- reactive, fires AFTER the damage is done. Also fires at fixed thresholds
//     regardless of remaining iteration budget.
//   - convergence pressure (agent.go 85%/95%): fires too late -- by 85%, the agent
//     has only 15% of budget left. Course correction at that point is nearly impossible.
//   - mid-point checkpoint (agent.go 60%): asks generic "on track?" with NO data --
//     the agent has no way to assess velocity without metrics.
//   - confidence.go: holistic quality score, not progress-rate prediction.
//   - fulfillment_gate.go: checks request-vs-work MATCH at completion, not whether
//     the agent will get to completion.
//
// This component tracks the ratio of productive iterations (containing edits, commands,
// commits -- same definition as overseer.productiveTools) to total iterations. At ~40%
// of the iteration budget, if the productive rate is below a threshold, it injects a
// DATA-DRIVEN forecast: specific numbers (productive actions, iterations elapsed,
// iterations remaining, projected shortfall). This gives the agent actionable context
// to change strategy before it's too late.
//
// Competitor mapping:
//   - Claude Code: no velocity forecasting; relies on the model's self-judgment
//   - Cursor: no iteration budget awareness
//   - Devin: SICA overseer reacts to stalls but doesn't forecast
//   - OpenHands: configurable max_iterations with no proactive warning
//   - Aider: no iteration budget (commits per-edit, no autonomous loop)
//
// Design constraints:
//   - Zero LLM cost (deterministic heuristic)
//   - Fires at most twice per run (at ~40% and ~60% of iteration budget)
//   - Research-mode aware: research tasks have lower productive rates by nature
//   - Only fires when there's a genuine velocity deficit (not false positives)

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// velocityFirstCheckFraction: inject the first forecast at this fraction of
	// the iteration budget. 40% is early enough to course-correct but late enough
	// to have meaningful data (at least velocityMinIterations samples).
	velocityFirstCheckFraction = 0.40

	// velocitySecondCheckFraction: inject a second forecast if velocity is still
	// low at 60% -- the point of no return for most tasks.
	velocitySecondCheckFraction = 0.60

	// velocityMinIterations: minimum total iteration budget for forecasting to
	// activate. Short runs (<20 iterations) are too short for meaningful velocity
	// analysis.
	velocityMinIterations = 20

	// velocityLowThreshold: if productive rate is below this fraction at the first
	// check, inject a forecast. Below 40% means the agent is spending more than
	// half its iterations on non-productive actions (reads, searches, retries).
	velocityLowThreshold = 0.40

	// velocityCriticalThreshold: at the second check (60%), the bar is higher --
	// if productive rate is still below 50%, the agent is very likely to run out.
	velocityCriticalThreshold = 0.50

	// velocityResearchLowThreshold: research tasks naturally have lower productive
	// rates (reading/searching IS the work). Use a more lenient threshold.
	velocityResearchLowThreshold = 0.20

	// velocityResearchCriticalThreshold: research-mode threshold for second check.
	velocityResearchCriticalThreshold = 0.30
)

// velocityForecastState tracks whether velocity forecasts have been injected.
type velocityForecastState struct {
	// firstCheckInjected tracks whether the 40% forecast has fired.
	firstCheckInjected bool

	// secondCheckInjected tracks whether the 60% forecast has fired.
	secondCheckInjected bool

	// productiveCount tracks iterations that contained productive tool calls.
	// Updated by the agent loop alongside overseer tracking.
	productiveCount int

	// totalCount tracks total iterations observed (for rate computation).
	totalCount int
}

func newVelocityForecastState() *velocityForecastState {
	return &velocityForecastState{}
}

func (v *velocityForecastState) reset() {
	v.firstCheckInjected = false
	v.secondCheckInjected = false
	v.productiveCount = 0
	v.totalCount = 0
}

// recordIteration records whether this iteration was productive (contained at
// least one productive tool call: edit, write, command, commit, etc.).
func (v *velocityForecastState) recordIteration(productive bool) {
	v.totalCount++
	if productive {
		v.productiveCount++
	}
}

// productiveRate returns the fraction of iterations that were productive.
func (v *velocityForecastState) productiveRate() float64 {
	if v.totalCount == 0 {
		return 0
	}
	return float64(v.productiveCount) / float64(v.totalCount)
}

// maybeForecast checks whether a velocity forecast should be injected.
// Returns guidance text if a forecast is warranted, "" otherwise.
//
// Parameters:
//   - currentIter: 1-based current iteration number
//   - maxIter: total iteration budget
//   - researchMode: whether the task is a research/analysis task
func (v *velocityForecastState) maybeForecast(currentIter, maxIter int, researchMode bool) string {
	if maxIter < velocityMinIterations {
		return ""
	}

	fraction := float64(currentIter) / float64(maxIter)
	rate := v.productiveRate()
	remaining := maxIter - currentIter

	// First check at ~40% of budget
	if fraction >= velocityFirstCheckFraction && !v.firstCheckInjected {
		threshold := velocityLowThreshold
		if researchMode {
			threshold = velocityResearchLowThreshold
		}

		if rate < threshold {
			v.firstCheckInjected = true
			// Project: at current rate, how many productive actions by end?
			projectedProductive := int(rate * float64(maxIter))
			// Estimate needed: assume task needs at least ~30% productive iterations
			// to complete. This is conservative.
			neededProductive := int(0.30 * float64(maxIter))
			shortfall := neededProductive - projectedProductive
			if shortfall < 1 {
				shortfall = 1
			}

			debug.Log("velocity-forecast", "first check: iter %d/%d (%.0f%%), productive rate %.0f%% (threshold %.0f%%), projected %d productive, need ~%d",
				currentIter, maxIter, fraction*100, rate*100, threshold*100, projectedProductive, neededProductive)

			taskWord := "productive"
			if researchMode {
				taskWord = "research-productive"
			}

			return fmt.Sprintf(
				"[velocity forecast] Iteration %d/%d (%d%% budget used). Your %s action rate is %d%% -- below the %d%% efficiency threshold. At this pace, you'll make ~%d %s actions total but need ~%d. You have %d iterations remaining. Strategy shift: reduce exploration, focus on the core changes needed to complete the task. Avoid re-reading files you've already inspected.",
				currentIter, maxIter, int(fraction*100),
				taskWord, int(rate*100), int(threshold*100),
				projectedProductive, taskWord, neededProductive,
				remaining,
			)
		}
	}

	// Second check at ~60% of budget (more urgent)
	if fraction >= velocitySecondCheckFraction && !v.secondCheckInjected {
		threshold := velocityCriticalThreshold
		if researchMode {
			threshold = velocityResearchCriticalThreshold
		}

		if rate < threshold {
			v.secondCheckInjected = true
			projectedProductive := int(rate * float64(maxIter))
			neededProductive := int(0.30 * float64(maxIter))
			shortfall := neededProductive - projectedProductive
			if shortfall < 1 {
				shortfall = 1
			}

			debug.Log("velocity-forecast", "second check: iter %d/%d (%.0f%%), productive rate %.0f%% (threshold %.0f%%), projected %d, need ~%d, shortfall %d",
				currentIter, maxIter, fraction*100, rate*100, threshold*100, projectedProductive, neededProductive, shortfall)

			taskWord := "productive"
			if researchMode {
				taskWord = "research-productive"
			}

			return fmt.Sprintf(
				"[velocity forecast] CRITICAL: iteration %d/%d (%d%% budget used). Your %s action rate is still only %d%%. Only %d iterations remain but you need ~%d more %s actions. You are projected to run out of budget. STOP exploring -- make the essential changes NOW, verify they work, and prepare to summarize.",
				currentIter, maxIter, int(fraction*100),
				taskWord, int(rate*100),
				remaining, shortfall, taskWord,
			)
		}
	}

	return ""
}
