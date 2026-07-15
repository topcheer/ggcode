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
func (c *TokenCalibrator) RecordSample(estimatedTokens, actualTokens int) {
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

	oldASCIIRatio, oldCJKRatio := c.asciiRatio, c.cjkRatio
	newASCIIRatio := c.asciiRatio * (1 - alpha + alpha*factor)
	newCJKRatio := c.cjkRatio * (1 - alpha + alpha*factor)

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
	debug.Log("context-calibrator", "adjusted sample=%d estimated=%d actual=%d factor=%.3f alpha=%.3f ascii=%.3f→%.3f cjk=%.3f→%.3f",
		c.samples, estimatedTokens, actualTokens, factor, alpha, oldASCIIRatio, newASCIIRatio, oldCJKRatio, newCJKRatio)
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
