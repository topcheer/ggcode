package context

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	defaultASCIIRatio   = 3.5 // chars per token for ASCII text
	defaultCJKRatio     = 1.5 // chars per token for CJK text
	calibWarmupSamples  = 5   // samples before calibration starts
	calibAdjustInterval = 3   // adjust every N samples after warmup
	asciiRatioMin       = 3.0
	asciiRatioMax       = 5.0
	cjkRatioMin         = 1.0
	cjkRatioMax         = 2.0
)

// TokenCalibrator self-calibrates the char/token ratio using API feedback.
// It uses incremental averaging: each new adjustment has decreasing weight,
// so the ratio converges over time without being dominated by outliers.
type TokenCalibrator struct {
	mu         sync.Mutex
	asciiRatio float64
	cjkRatio   float64
	samples    int
}

// NewTokenCalibrator creates a calibrator with default ratios.
func NewTokenCalibrator() *TokenCalibrator {
	return &TokenCalibrator{
		asciiRatio: defaultASCIIRatio,
		cjkRatio:   defaultCJKRatio,
	}
}

// RecordSample compares estimated tokens with actual API-reported tokens.
// If the estimation is consistently off, the ratios are adjusted via
// incremental averaging. The calibrator uses a warmup period and only
// adjusts at fixed intervals to avoid overreacting to individual samples.
//
// asciiChars/cjkChars describe the composition of the estimated text
// (#355): a pure-ASCII sample must only adjust asciiRatio, a pure-CJK
// sample only cjkRatio. Previously a single factor updated BOTH ratios,
// so unobserved parameters drifted to their clamp limits (11 pure-ASCII
// samples pushed cjkRatio 1.5→2.0, undercounting CJK estimates 25%).
// With no composition info (both zero), only asciiRatio updates — session
// text is predominantly code/tool output — and cjkRatio stays at default.
func (c *TokenCalibrator) RecordSample(estimatedTokens, actualTokens, asciiChars, cjkChars int) {
	if actualTokens <= 0 || estimatedTokens <= 0 {
		debug.Log("context-calibrator", "sample-skipped estimated=%d actual=%d", estimatedTokens, actualTokens)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.samples++

	// Warmup: don't adjust during first few samples
	if c.samples <= calibWarmupSamples {
		debug.Log("context-calibrator", "warmup sample=%d estimated=%d actual=%d ratio=%.3f/%.3f",
			c.samples, estimatedTokens, actualTokens, c.asciiRatio, c.cjkRatio)
		return
	}

	// Only adjust every Nth sample after warmup
	if (c.samples-calibWarmupSamples)%calibAdjustInterval != 0 {
		debug.Log("context-calibrator", "sample=%d estimated=%d actual=%d ratio=%.3f/%.3f (no-adjust)",
			c.samples, estimatedTokens, actualTokens, c.asciiRatio, c.cjkRatio)
		return
	}

	// Compute the correction factor: if estimated < actual, the ratio
	// (chars/token) is too high, so we need to decrease it.
	factor := float64(estimatedTokens) / float64(actualTokens)
	adjustmentNum := (c.samples - calibWarmupSamples) / calibAdjustInterval
	alpha := 1.0 / float64(adjustmentNum)

	// Split the factor by composition (#355): pure/mixed samples weight
	// each ratio's adjustment by the share of that script's characters.
	totalChars := asciiChars + cjkChars
	asciiShare, cjkShare := 1.0, 0.0 // no composition info: ASCII-only assumption
	if totalChars > 0 {
		asciiShare = float64(asciiChars) / float64(totalChars)
		cjkShare = float64(cjkChars) / float64(totalChars)
	}

	oldASCIIRatio, oldCJKRatio := c.asciiRatio, c.cjkRatio
	var newASCIIRatio, newCJKRatio float64
	if asciiShare > 0 {
		newASCIIRatio = c.asciiRatio * (1 - alpha*asciiShare + alpha*asciiShare*factor)
	} else {
		newASCIIRatio = c.asciiRatio // unobserved: frozen
	}
	if cjkShare > 0 {
		newCJKRatio = c.cjkRatio * (1 - alpha*cjkShare + alpha*cjkShare*factor)
	} else {
		newCJKRatio = c.cjkRatio // unobserved: frozen
	}

	// Clamp to safe ranges
	if newASCIIRatio < asciiRatioMin {
		newASCIIRatio = asciiRatioMin
	}
	if newASCIIRatio > asciiRatioMax {
		newASCIIRatio = asciiRatioMax
	}
	if newCJKRatio < cjkRatioMin {
		newCJKRatio = cjkRatioMin
	}
	if newCJKRatio > cjkRatioMax {
		newCJKRatio = cjkRatioMax
	}

	c.asciiRatio = newASCIIRatio
	c.cjkRatio = newCJKRatio
	debug.Log("context-calibrator", "adjusted sample=%d estimated=%d actual=%d asciiChars=%d cjkChars=%d factor=%.3f alpha=%.3f ascii=%.3f→%.3f cjk=%.3f→%.3f",
		c.samples, estimatedTokens, actualTokens, asciiChars, cjkChars, factor, alpha, oldASCIIRatio, newASCIIRatio, oldCJKRatio, newCJKRatio)
}

// ASCIICharsPerToken returns the calibrated chars/token ratio for ASCII text.
func (c *TokenCalibrator) ASCIICharsPerToken() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asciiRatio
}

// CJKCharsPerToken returns the calibrated chars/token ratio for CJK text.
func (c *TokenCalibrator) CJKCharsPerToken() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cjkRatio
}

// Reset returns the calibrator to default ratios and clears sample count.
func (c *TokenCalibrator) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asciiRatio = defaultASCIIRatio
	c.cjkRatio = defaultCJKRatio
	c.samples = 0
}
