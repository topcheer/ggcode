package agent

import "fmt"

// confidenceTrajectory (r75): trajectory-level confidence tracking on top of
// the sa-74 per-turn signal (StreamEvent.Confidence, mean token logprob;
// OpenAI logprobs / Gemini avgLogprobs, Anthropic nil).
//
// Frontier grounding: "Agentic Confidence Calibration" (arXiv:2601.15778,
// 2026) — static single-turn thresholds miss the defining failure mode of
// agentic systems, compounding errors along a trajectory. Process-level
// features (macro trend across turns + micro stability) expose sustained
// degradation that any individual turn looks fine in. This tracker is the
// minimal local version of that idea: it feeds the escalation ladder
// (r74's ask_user fallback) a calibrated, rate-limited signal instead of
// letting the agent compound low-confidence turns silently.
//
// Not a detector: it consumes an already-emitted provider signal and only
// escalates once per degradation episode, resetting after recovery.

const (
	// confWindow keeps the most recent per-turn logprob samples for trend
	// comparison. Small enough to stay interpretable, large enough to see
	// a downward slope over several turns.
	confWindow = 12

	// confHardLow is the existing single-turn alarm threshold (~8% avg
	// token probability), kept in the caller (agent.go).
	confHardLow = -2.5

	// confSustainedLow: per-turn level considered "degraded" for the
	// sustained-streak feature (~17% avg token probability — noticeably
	// worse than fluent text, but not yet the hard-alarm zone).
	confSustainedLow = -1.75

	// confSustainedTurns: consecutive degraded turns that constitute an
	// episode worth one escalation.
	confSustainedTurns = 3

	// confTrendDrop: logprob drop between the older and recent half of the
	// window that indicates a macro downward trend.
	confTrendDrop = 0.8

	// confRecover: a turn at or above this level ends the current
	// degradation episode, re-arming future escalation.
	confRecover = -0.75
)

type confidenceTrajectory struct {
	window        []float64 // recent per-turn mean token logprobs
	episodeActive bool      // one escalation notice per degradation episode
	turns         int       // total observed turns (diagnostics)
}

func newConfidenceTrajectory() *confidenceTrajectory {
	return &confidenceTrajectory{}
}

// observe records a turn's confidence and returns (notice, true) exactly
// when a *new* sustained-degradation episode should escalate. Single bad
// turns never fire here — that remains the caller's existing point check.
func (t *confidenceTrajectory) observe(conf float64) (string, bool) {
	t.turns++
	t.window = append(t.window, conf)
	if len(t.window) > confWindow {
		t.window = t.window[len(t.window)-confWindow:]
	}

	if conf >= confRecover {
		// Healthy turn: end any active episode so future degradation can
		// escalate again.
		t.episodeActive = false
		return "", false
	}

	if t.episodeActive {
		return "", false // already escalated this episode; stay quiet
	}

	n := len(t.window)
	if n < confSustainedTurns {
		return "", false
	}

	// Micro stability: confSustainedTurns consecutive degraded turns.
	sustained := true
	for _, c := range t.window[n-confSustainedTurns:] {
		if c >= confSustainedLow {
			sustained = false
			break
		}
	}

	// Macro dynamics: recent half of the window meaningfully below the
	// older half (downward trend), with the recent half itself negative.
	trend := false
	if n >= 6 {
		half := n / 2
		older, recent := t.window[:half], t.window[half:]
		om, rm := mean(older), mean(recent)
		trend = om-rm >= confTrendDrop && rm < 0
	}

	if !sustained && !trend {
		return "", false
	}

	t.episodeActive = true
	return fmt.Sprintf("[confidence] model confidence is degrading across turns "+
		"(last %d samples, now %.2f avg token logprob): compounding low-confidence "+
		"turns are a known predictor of trajectory failure (arXiv:2601.15778). "+
		"Re-verify the current approach or ask the user instead of continuing to build on uncertain output.",
		n, conf), true
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
