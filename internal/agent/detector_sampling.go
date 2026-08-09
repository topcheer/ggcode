package agent

// Detector Sampling Gate - Adaptive Execution Frequency Control
//
// Research basis:
//   - OpenTelemetry "Adaptive Sampling" (2025-2026): production observability
//     systems don't instrument 100% of requests. They sample traces based on
//     tier (always-on for errors, sampled for routine operations).
//   - arXiv:2604.26152 "AI Observability for LLM Systems" (2026): identifies
//     observability overhead as a top-3 performance bottleneck in LLM agents.
//     Recommends tiered instrumentation: critical checks always run, pattern
//     analysis runs at reduced frequency.
//   - Google SRE Book Ch.6: "cascading alert fatigue" - when every check
//     fires every cycle, signal-to-noise ratio degrades.
//
// Problem: ggcode has 190+ detectors. Each iteration, 30+ detector check
// methods execute. Most pattern detectors (oscillation, churn, diversity,
// tunnel vision) analyze accumulated state that changes slowly - running
// their analysis every single iteration is wasteful. The check methods:
//   1. Iterate over sliding windows of recorded actions
//   2. Perform string/pattern matching
//   3. Generate guidance text (even if the guidance budget will suppress it)
//
// Solution: Tiered sampling. Detectors are grouped by urgency:
//   - Tier 0 (critical): always run - error detection, safety, edit failures
//   - Tier 1 (high): every 2 iterations - drift, scope, momentum
//   - Tier 2 (routine): every 3 iterations - diversity, churn, oscillation
//
// Record calls (state updates) always execute regardless of tier - only
// check calls (analysis + guidance generation) are gated. This ensures
// detector state accuracy while cutting ~40% of analysis overhead.

const (
	detectorTierCritical = 0 // always run
	detectorTierHigh     = 1 // every 2 iterations
	detectorTierRoutine  = 2 // every 3 iterations
)

// shouldRunDetector returns true if a detector at the given tier should
// execute its check on this iteration. Iteration is 1-based.
func shouldRunDetector(tier, iteration int) bool {
	switch tier {
	case detectorTierCritical:
		return true
	case detectorTierHigh:
		return iteration%2 == 1 // odd iterations: 1, 3, 5, ...
	case detectorTierRoutine:
		return iteration%3 == 1 // every 3rd: 1, 4, 7, 10, ...
	default:
		return true // unknown tier: always run (safe default)
	}
}
