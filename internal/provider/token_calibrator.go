package provider

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// calibrateFirstTimeout is the max wait for the first synchronous calibration.
	// Longer than tokenCountTimeout (100ms) because it only happens once.
	calibrateFirstTimeout = 2 * time.Second

	// calibrateAsyncTimeout is the max wait for subsequent async calibrations.
	calibrateAsyncTimeout = 5 * time.Second

	// calibrateInterval is the minimum time between calibration calls.
	calibrateInterval = 30 * time.Second

	// calibrateMinCalls is the minimum number of CountTokens calls between calibrations.
	calibrateMinCalls = 10

	// ratioClampMin/Max prevent extreme ratios from destabilizing estimates.
	ratioClampMin = 0.3
	ratioClampMax = 3.0
)

// tokenCountCalibrator provides periodic real-API calibration for token
// counting. Instead of calling the count_tokens API on every invocation
// (which would add latency and risk rate limits), it periodically calls
// the real API to learn a correction ratio, then applies that ratio to
// fast local estimates between calibrations.
//
// Workflow:
//  1. First call: synchronous API calibration (up to calibrateFirstTimeout)
//  2. Subsequent calls: apply learned ratio locally (instant)
//  3. Every calibrateInterval + calibrateMinCalls: async re-calibration
//  4. On permanent API error (404/403): disable permanently, fall back to local
type tokenCountCalibrator struct {
	mu            sync.Mutex
	enabled       bool      // false = permanently disabled (API doesn't support it)
	ratio         float64   // correction factor: realTokens / estimatedTokens
	lastCalibrate time.Time // last successful calibration time
	callCount     int       // calls since last calibration trigger
}

// newTokenCountCalibrator creates a calibrator with default settings.
func newTokenCountCalibrator() *tokenCountCalibrator {
	return &tokenCountCalibrator{
		enabled: true,
		ratio:   1.0, // neutral ratio until calibrated
	}
}

// shouldCalibrate reports whether a new calibration round should run.
// Caller must hold mu.
func (c *tokenCountCalibrator) shouldCalibrate() bool {
	if !c.enabled {
		return false
	}
	c.callCount++
	if c.lastCalibrate.IsZero() {
		return true // first calibration
	}
	if c.callCount < calibrateMinCalls {
		return false
	}
	return time.Since(c.lastCalibrate) >= calibrateInterval
}

// currentRatio returns the current correction ratio (thread-safe).
func (c *tokenCountCalibrator) currentRatio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ratio
}

// applyResult updates the ratio based on a new (estimated, real) sample.
// Uses incremental averaging so the ratio converges over time without
// being dominated by outliers.
func (c *tokenCountCalibrator) applyResult(estimated, realTokens int) {
	if estimated <= 0 || realTokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	observedRatio := float64(realTokens) / float64(estimated)

	if c.ratio == 1.0 && c.lastCalibrate.IsZero() {
		// First calibration: accept directly.
		c.ratio = clampRatio(observedRatio)
	} else {
		// Incremental average: weight new sample at 30%, keep 70% of old ratio.
		// This prevents single samples from swinging the ratio too far.
		c.ratio = clampRatio(c.ratio*0.7 + observedRatio*0.3)
	}

	c.lastCalibrate = time.Now()
	c.callCount = 0

	debug.Log("provider-calibrator", "ratio updated: estimated=%d real=%d observed=%.3f newRatio=%.3f",
		estimated, realTokens, observedRatio, c.ratio)
}

// disable permanently turns off remote calibration (e.g., endpoint returns 404).
func (c *tokenCountCalibrator) disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled {
		c.enabled = false
		c.ratio = 1.0 // fall back to raw local estimate
		debug.Log("provider-calibrator", "remote counting disabled (endpoint does not support count_tokens)")
	}
}

// clampRatio constrains the ratio to a safe range to prevent destabilization.
func clampRatio(r float64) float64 {
	if r < ratioClampMin {
		return ratioClampMin
	}
	if r > ratioClampMax {
		return ratioClampMax
	}
	return r
}
