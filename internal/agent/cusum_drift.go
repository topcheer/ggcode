package agent

import (
	"fmt"
	"strings"
	"sync"
)

// cusumDriftState implements CUSUM (Cumulative Sum) based behavioral drift
// detection - a sequential change-detection algorithm from statistical process
// control and cognitive science metacognition research.
//
// Unlike all other detectors in ggcode which use point-in-time threshold
// checks (count > N, rate > X), CUSUM accumulates small deviations from a
// baseline. Each individual observation may be within normal bounds, but the
// cumulative sum reveals a gradual directional shift that no single check
// would catch. This is the foundational pattern for "drift detection" in the
// metacognition and adaptive control literature (Nelson-Narens 1990,
// Kephart-Chess MAPE-K 2003, CUSUM Page 1954).
//
// The detector tracks multiple behavioral signals simultaneously:
//
//   - Error rate drift: tool calls that fail are accumulating faster than
//     the baseline established early in the session.
//   - Read-to-write ratio drift: the agent is shifting toward increasingly
//     exploratory (read-heavy) behavior without commensurate output.
//   - Token velocity drift: per-call token consumption is gradually
//     increasing (verbosity creep).
//
// This is distinct from:
//   - verbosity_drift: tracks token-to-edit ratio (productivity), not
//     cumulative statistical drift across multiple dimensions.
//   - analysis_paralysis: binary check (zero modify tools), not gradual.
//   - error_strategy_loop: detects repeated identical errors, not gradual
//     error-rate escalation.
type cusumDriftState struct {
	mu sync.Mutex

	// CUSUM statistics for each tracked signal.
	errorRateCUSUM     float64
	readWriteCUSUM     float64
	tokenVelocityCUSUM float64

	// Baseline estimates (running averages established during warmup).
	baselineErrorRate float64
	baselineReadWrite float64
	baselineTokenVel  float64

	// Warmup samples collected before baseline is established.
	warmupErrorRates []float64
	warmupReadWrite  []float64
	warmupTokenVels  []float64
	warmupCount      int

	// Accumulated totals across all tool calls.
	totalToolCalls   int
	totalFailedCalls int
	totalReadCalls   int
	totalWriteCalls  int
	cumulativeTokens int

	// Whether baseline has been established.
	baselined bool

	// Configuration.
	warmupPeriod   int     // samples before baseline locks in
	checkInterval  int     // tool calls between CUSUM evaluations
	threshold      float64 // CUSUM value that triggers alert
	driftAllowance float64 // tolerance band (dead band to prevent chatter)

	// Alert state.
	maxAlerts   int
	alertCount  int
	fired       bool
	lastAlertTc int // last tool call index when alert fired
}

type cusumDriftSignal struct {
	name      string
	cusum     float64
	baseline  float64
	current   float64
	threshold float64
}

// cusumRecord captures one tool call's raw signals.
type cusumRecord struct {
	failed     bool
	isRead     bool
	isWrite    bool
	tokenDelta int
}

func newCusumDriftState() *cusumDriftState {
	return &cusumDriftState{
		warmupPeriod:   5,   // establish baseline from first 5 samples
		checkInterval:  3,   // evaluate CUSUM every 3 tool calls
		threshold:      3.0, // CUSUM alert threshold (standardized units)
		driftAllowance: 0.3, // dead band: ignore deviations < 0.3 sigma
		maxAlerts:      2,   // cap alerts per run
	}
}

func (c *cusumDriftState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errorRateCUSUM = 0
	c.readWriteCUSUM = 0
	c.tokenVelocityCUSUM = 0
	c.baselineErrorRate = 0
	c.baselineReadWrite = 0
	c.baselineTokenVel = 0
	c.warmupErrorRates = nil
	c.warmupReadWrite = nil
	c.warmupTokenVels = nil
	c.warmupCount = 0
	c.totalToolCalls = 0
	c.totalFailedCalls = 0
	c.totalReadCalls = 0
	c.totalWriteCalls = 0
	c.cumulativeTokens = 0
	c.baselined = false
	c.alertCount = 0
	c.fired = false
	c.lastAlertTc = 0
}

// record observes one tool call and updates CUSUM statistics.
// Returns a guidance message if drift is detected, empty string otherwise.
func (c *cusumDriftState) record(rec cusumRecord) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalToolCalls++
	if rec.failed {
		c.totalFailedCalls++
	}
	if rec.isRead {
		c.totalReadCalls++
	}
	if rec.isWrite {
		c.totalWriteCalls++
	}
	c.cumulativeTokens += rec.tokenDelta

	// Only evaluate at check intervals.
	if c.totalToolCalls%c.checkInterval != 0 {
		return ""
	}

	// Compute current aggregate signal values.
	curErrorRate := float64(c.totalFailedCalls) / float64(c.totalToolCalls)
	totalRW := c.totalReadCalls + c.totalWriteCalls
	curReadWrite := 0.5
	if totalRW > 0 {
		curReadWrite = float64(c.totalReadCalls) / float64(totalRW)
	}
	curTokenVel := float64(c.cumulativeTokens) / float64(c.totalToolCalls)

	// During warmup, collect samples to establish baseline.
	if !c.baselined {
		c.warmupErrorRates = append(c.warmupErrorRates, curErrorRate)
		c.warmupReadWrite = append(c.warmupReadWrite, curReadWrite)
		c.warmupTokenVels = append(c.warmupTokenVels, curTokenVel)
		c.warmupCount++

		if c.warmupCount >= c.warmupPeriod {
			c.baselineErrorRate = cusumMean(c.warmupErrorRates)
			c.baselineReadWrite = cusumMean(c.warmupReadWrite)
			c.baselineTokenVel = cusumMean(c.warmupTokenVels)
			if c.baselineTokenVel < 1 {
				c.baselineTokenVel = 1000
			}
			c.baselined = true
		}
		return ""
	}

	// Post-warmup: update one-sided CUSUM for each signal.
	// C_n = C_{n-1} + (x - baseline) when deviation exceeds dead band;
	// otherwise exponential decay toward zero (prevents stale accumulation).

	errDev := curErrorRate - c.baselineErrorRate
	if errDev > c.driftAllowance {
		c.errorRateCUSUM += errDev
	} else {
		c.errorRateCUSUM *= 0.7
	}

	rwDev := curReadWrite - c.baselineReadWrite
	if rwDev > c.driftAllowance {
		c.readWriteCUSUM += rwDev
	} else {
		c.readWriteCUSUM *= 0.7
	}

	tvDev := (curTokenVel - c.baselineTokenVel) / c.baselineTokenVel
	if tvDev > c.driftAllowance {
		c.tokenVelocityCUSUM += tvDev
	} else {
		c.tokenVelocityCUSUM *= 0.7
	}

	// Check if any CUSUM crossed threshold.
	if c.fired || c.alertCount >= c.maxAlerts {
		return ""
	}
	if c.totalToolCalls-c.lastAlertTc < 6 {
		return "" // cooldown
	}

	var alerts []cusumDriftSignal
	if c.errorRateCUSUM >= c.threshold {
		alerts = append(alerts, cusumDriftSignal{
			name: "error-rate", cusum: c.errorRateCUSUM, baseline: c.baselineErrorRate,
			current: curErrorRate, threshold: c.threshold,
		})
	}
	if c.readWriteCUSUM >= c.threshold {
		alerts = append(alerts, cusumDriftSignal{
			name: "read-heavy", cusum: c.readWriteCUSUM, baseline: c.baselineReadWrite,
			current: curReadWrite, threshold: c.threshold,
		})
	}
	if c.tokenVelocityCUSUM >= c.threshold {
		alerts = append(alerts, cusumDriftSignal{
			name: "token-velocity", cusum: c.tokenVelocityCUSUM, baseline: c.baselineTokenVel,
			current: curTokenVel, threshold: c.threshold,
		})
	}

	if len(alerts) == 0 {
		return ""
	}

	c.alertCount++
	c.lastAlertTc = c.totalToolCalls
	if c.alertCount >= c.maxAlerts {
		c.fired = true
	}

	return c.formatAlert(alerts)
}

func (c *cusumDriftState) formatAlert(signals []cusumDriftSignal) string {
	var sb strings.Builder

	sb.WriteString("[CUSUM Drift] Gradual behavioral drift detected across ")
	if len(signals) == 1 {
		sb.WriteString("a signal")
	} else {
		sb.WriteString("multiple signals")
	}
	sb.WriteString(" - no single tool call crossed a threshold, but cumulative ")
	sb.WriteString("deviation from your established baseline reveals a directional trend:\n")

	for _, sig := range signals {
		sb.WriteString(fmt.Sprintf("  - %s: CUSUM=%.2f (threshold %.1f), ", sig.name, sig.cusum, sig.threshold))
		sb.WriteString(fmt.Sprintf("baseline=%.2f -> current=%.2f\n", sig.baseline, sig.current))
	}

	sb.WriteString("\n")
	sb.WriteString("This drift pattern (CUSUM cumulative sum, Page 1954) indicates a gradual ")
	sb.WriteString("quality degradation that point-in-time checks miss. The trend has been ")
	sb.WriteString("building over recent tool calls.\n")
	sb.WriteString("Recommendation: pause to reassess your approach - the gradual shift suggests ")
	sb.WriteString("a strategy that worked initially is becoming less effective. Consider whether ")
	sb.WriteString("a different tactic would arrest the trend before it compounds further.")

	return sb.String()
}

func cusumMean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, val := range vals {
		sum += val
	}
	return sum / float64(len(vals))
}
