package context

import (
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	defaultASCIIRatio     = 3.5
	defaultCJKRatio       = 1.0
	calibWarmupSamples    = 5
	calibAdjustInterval   = 3
	asciiRatioMin         = 3.0
	asciiRatioMax         = 5.0
	cjkRatioMin           = 0.6
	cjkRatioMax           = 2.0
	latinExtDominantShare = 0.5
)

type TokenCalibrator struct {
	mu            sync.Mutex
	lastEstimated int
	asciiRatio    float64
	cjkRatio      float64
	samples       int
}

func NewTokenCalibrator() *TokenCalibrator {
	return &TokenCalibrator{
		asciiRatio: defaultASCIIRatio,
		cjkRatio:   defaultCJKRatio,
	}
}

func (c *TokenCalibrator) RecordSample(estimatedTokens, actualTokens, asciiChars, cjkChars, latinExtChars int) {
	if actualTokens <= 0 || estimatedTokens <= 0 {
		debug.Log("context-calibrator", "sample-skipped estimated=%d actual=%d", estimatedTokens, actualTokens)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.samples++
	c.lastEstimated = estimatedTokens

	if c.samples <= calibWarmupSamples {
		debug.Log("context-calibrator", "warmup sample=%d estimated=%d actual=%d ratio=%.3f/%.3f",
			c.samples, estimatedTokens, actualTokens, c.asciiRatio, c.cjkRatio)
		return
	}

	if (c.samples-calibWarmupSamples)%calibAdjustInterval != 0 {
		debug.Log("context-calibrator", "sample=%d estimated=%d actual=%d ratio=%.3f/%.3f (no-adjust)",
			c.samples, estimatedTokens, actualTokens, c.asciiRatio, c.cjkRatio)
		return
	}

	factor := float64(estimatedTokens) / float64(actualTokens)
	adjustmentNum := (c.samples - calibWarmupSamples) / calibAdjustInterval
	alpha := 1.0 / float64(adjustmentNum)

	totalChars := asciiChars + cjkChars + latinExtChars
	asciiShare, cjkShare := 1.0, 0.0
	if totalChars > 0 {
		latinExtShare := float64(latinExtChars) / float64(totalChars)
		if latinExtShare > latinExtDominantShare {
			debug.Log("context-calibrator", "sample-frozen: latinExt-dominant composition (%.0f%%) — residual belongs to the fixed latinExt tier (see #649)",
				latinExtShare*100)
			return
		}
		asciiShare = float64(asciiChars) / float64(totalChars)
		cjkShare = float64(cjkChars) / float64(totalChars)
	}

	oldASCIIRatio, oldCJKRatio := c.asciiRatio, c.cjkRatio
	var newASCIIRatio, newCJKRatio float64
	if asciiShare > 0 {
		newASCIIRatio = c.asciiRatio * (1 - alpha*asciiShare + alpha*asciiShare*factor)
	} else {
		newASCIIRatio = c.asciiRatio
	}
	if cjkShare > 0 {
		newCJKRatio = c.cjkRatio * (1 - alpha*cjkShare + alpha*cjkShare*factor)
	} else {
		newCJKRatio = c.cjkRatio
	}

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
	debug.Log("context-calibrator", "adjusted sample=%d estimated=%d actual=%d asciiChars=%d cjkChars=%d latinExtChars=%d factor=%.3f alpha=%.3f ascii=%.3f→%.3f cjk=%.3f→%.3f",
		c.samples, estimatedTokens, actualTokens, asciiChars, cjkChars, latinExtChars, factor, alpha, oldASCIIRatio, newASCIIRatio, oldCJKRatio, newCJKRatio)
}

func (c *TokenCalibrator) ASCIICharsPerToken() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asciiRatio
}

func (c *TokenCalibrator) CJKCharsPerToken() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cjkRatio
}

func (c *TokenCalibrator) LastEstimated() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastEstimated
}

func (c *TokenCalibrator) SampleCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.samples
}

func (c *TokenCalibrator) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asciiRatio = defaultASCIIRatio
	c.cjkRatio = defaultCJKRatio
	c.samples = 0
}
