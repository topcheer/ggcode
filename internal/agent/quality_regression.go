package agent

import (
	"fmt"
	"math"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	regressionMinHistory = 3

	regressionWindow = 10

	regressionScoreDrop = 0.15

	regressionIterationMultiple = 1.6

	regressionErrorMultiple = 1.8
	regressionErrorFloor    = 0.15
)

type RegressionSeverity string

const (
	SeverityNone     RegressionSeverity = "none"
	SeverityMinor    RegressionSeverity = "minor"
	SeverityModerate RegressionSeverity = "moderate"
	SeveritySevere   RegressionSeverity = "severe"
)

type RegressionReport struct {
	Detected     bool
	Severity     RegressionSeverity
	Provider     string
	Model        string
	CurrentScore float64
	BaselineMean float64
	BaselineMin  float64
	RunCount     int
	Signals      []string
}

type baselineStats struct {
	count       int
	meanScore   float64
	minScore    float64
	meanIter    float64
	meanErrRate float64
}

func (s *ResponseQualityScorer) DetectRegression() RegressionReport {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.runs) == 0 {
		return RegressionReport{}
	}
	current := s.runs[len(s.runs)-1]

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
		return rep
	}

	start := len(prior) - regressionWindow
	if start < 0 {
		start = 0
	}
	window := prior[start:]
	bs := computeBaseline(window)
	rep.BaselineMean = bs.meanScore
	rep.BaselineMin = bs.minScore

	var signals []string

	scoreDrop := bs.meanScore - current.Score
	if scoreDrop >= regressionScoreDrop {
		signals = append(signals, fmt.Sprintf(
			"quality score %.3f vs baseline mean %.3f (drop %.3f)",
			current.Score, bs.meanScore, scoreDrop))
	}

	if bs.meanIter > 0 && current.Signals.IterationRatio > bs.meanIter*regressionIterationMultiple {
		signals = append(signals, fmt.Sprintf(
			"iteration ratio %.2fx vs baseline %.2fx (%.1fx baseline)",
			current.Signals.IterationRatio, bs.meanIter,
			current.Signals.IterationRatio/bs.meanIter))
	}

	errThreshold := math.Max(regressionErrorFloor, bs.meanErrRate*regressionErrorMultiple)
	if current.Signals.ErrorRate > errThreshold {
		if bs.meanErrRate < regressionErrorFloor {
			signals = append(signals, fmt.Sprintf(
				"error rate %.2f emerged (baseline %.2f)",
				current.Signals.ErrorRate, bs.meanErrRate))
		} else {
			signals = append(signals, fmt.Sprintf(
				"error rate %.2f vs baseline %.2f (%.1fx baseline)",
				current.Signals.ErrorRate, bs.meanErrRate,
				current.Signals.ErrorRate/bs.meanErrRate))
		}
	}

	if len(signals) == 0 {
		return rep
	}

	rep.Detected = true
	rep.Signals = signals
	rep.Severity = classifyRegression(scoreDrop, current, bs)
	return rep
}

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

func classifyRegression(scoreDrop float64, current QualityEntry, bs baselineStats) RegressionSeverity {

	switch {
	case scoreDrop >= 0.35:
		return SeveritySevere
	case scoreDrop >= 0.25:
		return SeverityModerate
	case scoreDrop >= regressionScoreDrop:
		return SeverityMinor
	}

	if bs.meanIter > 0 {
		iterMult := current.Signals.IterationRatio / bs.meanIter
		switch {
		case iterMult >= 2.0:
			return SeverityModerate
		case iterMult >= regressionIterationMultiple:
			return SeverityMinor
		}
	}

	if bs.meanErrRate > 0 {
		if current.Signals.ErrorRate >= bs.meanErrRate*regressionErrorMultiple {
			return SeverityMinor
		}
	} else if current.Signals.ErrorRate >= regressionErrorFloor {
		return SeverityMinor
	}

	if current.Score < bs.minScore-0.05 {
		return SeverityMinor
	}
	return SeverityNone
}

func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

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

func (s *ResponseQualityScorer) maybeDetectRegression() {
	if s == nil {
		return
	}
	rep := s.DetectRegression()
	if !rep.Detected {
		return
	}

	s.mu.Lock()
	s.latestRegression = rep
	s.mu.Unlock()
	debug.Log("agent", "quality regression detected: %s", rep.Format())
}
