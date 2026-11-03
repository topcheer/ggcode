package provider

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	calibrateFirstTimeout = 2 * time.Second

	calibrateAsyncTimeout = 5 * time.Second

	calibrateInterval = 30 * time.Second

	calibrateMinCalls = 10

	ratioClampMin = 0.3
	ratioClampMax = 3.0

	calibrateFailureBackoff = 5 * time.Second

	calibrateFailureBackoffMax = 60 * time.Second

	calibrateMaxConsecutiveFailures = 5
)

type tokenCountCalibrator struct {
	mu            sync.Mutex
	enabled       bool
	ratio         float64
	lastCalibrate time.Time
	callCount     int
	lastFailure   time.Time
	failCount     int
}

func newTokenCountCalibrator() *tokenCountCalibrator {
	return &tokenCountCalibrator{
		enabled: true,
		ratio:   1.0,
	}
}

func (c *tokenCountCalibrator) shouldCalibrate() bool {
	if !c.enabled {
		return false
	}

	if !c.lastFailure.IsZero() {
		backoff := calibrateFailureBackoff << uint(c.failCount-1)
		if backoff > calibrateFailureBackoffMax || backoff <= 0 {
			backoff = calibrateFailureBackoffMax
		}
		if time.Since(c.lastFailure) < backoff {
			return false
		}
	}
	c.callCount++
	if c.lastCalibrate.IsZero() {
		return true
	}
	if c.callCount < calibrateMinCalls {
		return false
	}
	return time.Since(c.lastCalibrate) >= calibrateInterval
}

func (c *tokenCountCalibrator) currentRatio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ratio
}

func (c *tokenCountCalibrator) applyResult(estimated, realTokens int) {
	if estimated <= 0 || realTokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	observedRatio := float64(realTokens) / float64(estimated)

	if c.ratio == 1.0 && c.lastCalibrate.IsZero() {

		c.ratio = clampRatio(observedRatio)
	} else {

		c.ratio = clampRatio(c.ratio*0.7 + observedRatio*0.3)
	}

	c.lastCalibrate = time.Now()
	c.callCount = 0
	c.lastFailure = time.Time{}
	c.failCount = 0

	debug.Log("provider-calibrator", "ratio updated: estimated=%d real=%d observed=%.3f newRatio=%.3f",
		estimated, realTokens, observedRatio, c.ratio)
}

func (c *tokenCountCalibrator) recordSkip() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	c.lastCalibrate = time.Now()
}

func (c *tokenCountCalibrator) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	c.failCount++
	c.lastFailure = time.Now()
	if c.failCount >= calibrateMaxConsecutiveFailures {
		c.enabled = false
		c.ratio = 1.0
		debug.Log("provider-calibrator", "remote counting disabled after %d consecutive calibration failures", c.failCount)
		return
	}
	backoff := calibrateFailureBackoff << uint(c.failCount-1)
	if backoff > calibrateFailureBackoffMax || backoff <= 0 {
		backoff = calibrateFailureBackoffMax
	}
	debug.Log("provider-calibrator", "calibration failed (%d consecutive), backing off %s", c.failCount, backoff)
}

func (c *tokenCountCalibrator) disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled {
		c.enabled = false
		c.ratio = 1.0
		debug.Log("provider-calibrator", "remote counting disabled (endpoint does not support count_tokens)")
	}
}

func clampRatio(r float64) float64 {
	if r < ratioClampMin {
		return ratioClampMin
	}
	if r > ratioClampMax {
		return ratioClampMax
	}
	return r
}
