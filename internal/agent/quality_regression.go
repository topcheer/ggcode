package agent

// Quality Regression Detector -- Eval-Driven Development regression detection.
//
// Background: Eval-Driven Development (LangSmith Evals, Braintrust, OpenAI Evals
// Framework) is the 2025-2026 core methodology for systematically measuring AI
// agent quality and driving continuous improvement. A central capability of
// every eval platform is REGRESSION DETECTION: flagging when a run's quality
// has dropped significantly below the historical baseline, so teams can catch
// quality degradation early rather than discovering it weeks later.
//
// Gap in ggcode: ResponseQualityScorer records a per-run QualityEntry and can
// compare providers A/B, but it NEVER compares the latest run against its own
// rolling historical baseline. So a slow quality erosion (e.g. from a config
// change, a model swap, or accumulating context bloat) goes completely silent.
// This detector fills that gap.
//
// Design: deterministic, zero LLM overhead. After each scored run it builds a
// rolling baseline (mean over the previous N runs for the SAME provider/model
// pair) and classifies the current run's deviation. It surfaces:
//   - Overall score regression (current << baseline mean)
//   - Iteration inflation (current iterations >> baseline mean) -- the single
//     most actionable leading indicator of degrading agent efficiency
//   - Error-rate regression (current error rate >> baseline mean)
//
// Severity tiers mirror the gate/check pattern used elsewhere in this package:
// advisory only, never blocks the run. The latest report is stored on the
// scorer so any reporting/telemetry surface can retrieve it. Detection also
// emits a debug.Log so regressions are observable without wiring.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// regressionMinHistory: minimum prior runs (same provider/model) needed to
	// form a trustworthy baseline. Below this, variance noise dominates.
	regressionMinHistory = 3

	// regressionWindow: how many most-recent prior runs form the baseline.
	regressionWindow = 10

	// regressionScoreDrop: an absolute score drop (0-1 scale) at or beyond this
	// versus the baseline mean flags a regression.
	regressionScoreDrop = 0.15

	// regressionIterationMultiple: if current iterations exceed baseline-mean
	// iterations by this multiple, flag iteration inflation.
	regressionIterationMultiple = 1.6

	// regressionErrorMultiple: if current error-rate exceeds baseline-mean
	// error-rate by this multiple (and is non-trivial), flag error regression.
	regressionErrorMultiple = 1.8
	regressionErrorFloor    = 0.15 // below this absolute error rate, ignore
)

// RegressionSeverity classifies how far below baseline the current run fell.
type RegressionSeverity string

const (
	SeverityNone     RegressionSeverity = "none"
	SeverityMinor    RegressionSeverity = "minor"
	SeverityModerate RegressionSeverity = "moderate"
	SeveritySevere   RegressionSeverity = "severe"
)

// RegressionReport summarizes a quality-regression detection for one run.
// Returned by ResponseQualityScorer.DetectRegression.
type RegressionReport struct {
	Detected     bool
	Severity     RegressionSeverity
	Provider     string
	Model        string
	CurrentScore float64
	BaselineMean float64
	BaselineMin  float64
	RunCount     int // prior runs in the baseline
	Signals      []string
}

// baselineStats holds aggregate statistics over a window of prior runs.
type baselineStats struct {
	count       int
	meanScore   float64
	minScore    float64
	meanIter    float64 // mean IterationRatio across baseline
	meanErrRate float64 // mean ErrorRate across baseline
}

// DetectRegression compares the most recently scored run against the rolling
// baseline of prior runs for the same provider/model pair. Returns a report
// describing whether and how severely the run regressed.
//
// The "current" run is the last entry appended. The baseline is built from the
// preceding runs matching the same provider/model, up to regressionWindow.
// If there are fewer than regressionMinHistory prior runs, detection is
// skipped (Detected=false) because the baseline is not yet trustworthy.
func (s *ResponseQualityScorer) DetectRegression() RegressionReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.runs) == 0 {
		return RegressionReport{}
	}
	current := s.runs[len(s.runs)-1]

	// Collect prior runs for the same provider/model (excluding current).
	var prior []QualityEntry
	for i := 0; i < len(s.runs)-1; i++ {
		r := s.runs[i]
		if r.Provider == current.Provider && r.Model == current.Model {
			prior = append(prior, r)
		}
	}

	rep := RegressionReport{
		Provider:     current.Provider,
		Model:        current.Model,
		CurrentScore: current.Score,
		RunCount:     len(prior),
	}

	if len(prior) < regressionMinHistory {
		return rep // insufficient history -- cannot trust a baseline
	}

	// Use only the most recent regressionWindow prior runs for the baseline.
	start := len(prior) - regressionWindow
	if start < 0 {
		start = 0
	}
	window := prior[start:]
	bs := computeBaseline(window)
	rep.BaselineMean = bs.meanScore
	rep.BaselineMin = bs.minScore

	var signals []string

	// 1) Overall score regression.
	scoreDrop := bs.meanScore - current.Score
	if scoreDrop >= regressionScoreDrop {
		signals = append(signals, fmt.Sprintf(
			"quality score %.3f vs baseline mean %.3f (drop %.3f)",
			current.Score, bs.meanScore, scoreDrop))
	}

	// 2) Iteration inflation: leading indicator of degrading efficiency.
	// Compare current IterationRatio to baseline mean.
	if bs.meanIter > 0 && current.Signals.IterationRatio > bs.meanIter*regressionIterationMultiple {
		signals = append(signals, fmt.Sprintf(
			"iteration ratio %.2fx vs baseline %.2fx (%.1fx baseline)",
			current.Signals.IterationRatio, bs.meanIter,
			current.Signals.IterationRatio/bs.meanIter))
	}

	// 3) Error-rate regression.
	if bs.meanErrRate < regressionErrorFloor {
		// baseline was clean; any meaningful current error rate is a signal
		if current.Signals.ErrorRate > regressionErrorFloor {
			signals = append(signals, fmt.Sprintf(
				"error rate %.2f emerged (baseline %.2f)",
				current.Signals.ErrorRate, bs.meanErrRate))
		}
	} else if current.Signals.ErrorRate > bs.meanErrRate*regressionErrorMultiple {
		signals = append(signals, fmt.Sprintf(
			"error rate %.2f vs baseline %.2f (%.1fx baseline)",
			current.Signals.ErrorRate, bs.meanErrRate,
			current.Signals.ErrorRate/bs.meanErrRate))
	}

	if len(signals) == 0 {
		return rep
	}

	rep.Detected = true
	rep.Signals = signals
	rep.Severity = classifyRegression(scoreDrop, current, bs)
	return rep
}

// computeBaseline aggregates a window of QualityEntry into baselineStats.
func computeBaseline(entries []QualityEntry) baselineStats {
	if len(entries) == 0 {
		return baselineStats{}
	}
	var scoreSum, iterSum, errSum float64
	minScore := entries[0].Score
	for _, e := range entries {
		scoreSum += e.Score
		iterSum += e.Signals.IterationRatio
		errSum += e.Signals.ErrorRate
		if e.Score < minScore {
			minScore = e.Score
		}
	}
	n := float64(len(entries))
	return baselineStats{
		count:       len(entries),
		meanScore:   round3(scoreSum / n),
		minScore:    minScore,
		meanIter:    iterSum / n,
		meanErrRate: errSum / n,
	}
}

// classifyRegression assigns a severity tier based on score drop magnitude and
// whether the run also underperformed the historical minimum.
func classifyRegression(scoreDrop float64, current QualityEntry, bs baselineStats) RegressionSeverity {
	// Score-drop based tiers (absolute 0-1 scale).
	switch {
	case scoreDrop >= 0.35:
		return SeveritySevere
	case scoreDrop >= 0.25:
		return SeverityModerate
	case scoreDrop >= regressionScoreDrop:
		return SeverityMinor
	}

	// Even without a large overall drop, if iterations inflated severely OR the
	// run fell below the historical minimum score, that is at least minor.
	if bs.meanIter > 0 && current.Signals.IterationRatio >= bs.meanIter*2.0 {
		return SeverityModerate
	}
	if current.Score < bs.minScore-0.05 {
		return SeverityMinor
	}
	return SeverityNone
}

// round3 rounds to 3 decimal places (matches the scorer's precision).
func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

// Format returns a human-readable summary of the regression report.
// Empty if no regression was detected.
func (r RegressionReport) Format() string {
	if !r.Detected {
		return ""
	}
	var b strings.Builder
	label := r.Provider + "/" + r.Model
	if label == "/" {
		label = "(unknown provider)"
	}
	fmt.Fprintf(&b, "[quality regression: %s] %s run scored %.3f vs baseline %.3f (n=%d): ",
		r.Severity, label, r.CurrentScore, r.BaselineMean, r.RunCount)
	b.WriteString(strings.Join(r.Signals, "; "))
	b.WriteString(". Investigate recent config/model/context changes that may have degraded agent quality.")
	return b.String()
}

// maybeDetectRegression is called from the reflection path after a run is
// scored. It detects regression against the rolling baseline and, when found,
// emits a debug log so the degradation is observable. Non-fatal: never blocks.
func (s *ResponseQualityScorer) maybeDetectRegression() {
	if s == nil {
		return
	}
	rep := s.DetectRegression()
	if !rep.Detected {
		return
	}
	// Store latest report for any reporting surface.
	s.mu.Lock()
	s.latestRegression = rep
	s.mu.Unlock()
	debug.Log("agent", "quality regression detected: %s", rep.Format())
}
