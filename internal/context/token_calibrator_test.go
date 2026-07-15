package context

import (
	"testing"
)

func TestTokenCalibrator_DefaultRatios(t *testing.T) {
	c := NewTokenCalibrator()
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("default ASCII ratio = %v, want 3.5", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.5 {
		t.Errorf("default CJK ratio = %v, want 1.5", got)
	}
}

func TestTokenCalibrator_RecordSample_AdjustsRatio(t *testing.T) {
	c := NewTokenCalibrator()
	// Feed warmup samples (should not adjust)
	for i := 0; i < 5; i++ {
		c.RecordSample(1000, 1200) // estimated 1000, actual 1200
	}
	// Ratio should still be default after warmup
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Fatalf("after warmup, ASCII ratio = %v, want 3.5", got)
	}
	// Feed 3 more samples to trigger first adjustment (sample 8)
	for i := 0; i < 3; i++ {
		c.RecordSample(1000, 1200)
	}
	// estimated < actual → ratio should decrease (chars per token goes down)
	// factor = 1000/1200 = 0.833, alpha = 1.0, new ratio = 3.5 * 0.833 = 2.915
	if got := c.ASCIICharsPerToken(); got >= 3.5 {
		t.Errorf("after calibration, ASCII ratio = %v, should be < 3.5 (was underestimating)", got)
	}
}

func TestTokenCalibrator_WarmupNoAdjust(t *testing.T) {
	c := NewTokenCalibrator()
	for i := 0; i < calibWarmupSamples; i++ {
		c.RecordSample(500, 2000) // extreme but should be ignored
	}
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("during warmup, ASCII ratio = %v, want 3.5 (no adjustment)", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.5 {
		t.Errorf("during warmup, CJK ratio = %v, want 1.5 (no adjustment)", got)
	}
}

func TestTokenCalibrator_RatioClamped(t *testing.T) {
	c := NewTokenCalibrator()
	// Bypass warmup
	for i := 0; i < calibWarmupSamples; i++ {
		c.RecordSample(100, 1) // extreme: estimated >> actual
	}
	// Force several adjustments
	for i := 0; i < 30; i++ {
		c.RecordSample(100, 1) // factor = 100, wants to push ratio very high
	}
	if got := c.ASCIICharsPerToken(); got > asciiRatioMax {
		t.Errorf("ASCII ratio = %v, exceeds max %v", got, asciiRatioMax)
	}
	if got := c.CJKCharsPerToken(); got > cjkRatioMax {
		t.Errorf("CJK ratio = %v, exceeds max %v", got, cjkRatioMax)
	}
	// Also test lower clamp
	c2 := NewTokenCalibrator()
	for i := 0; i < calibWarmupSamples; i++ {
		c2.RecordSample(1, 10000)
	}
	for i := 0; i < 30; i++ {
		c2.RecordSample(1, 10000) // factor = 0.0001, wants to push ratio very low
	}
	if got := c2.ASCIICharsPerToken(); got < asciiRatioMin {
		t.Errorf("ASCII ratio = %v, below min %v", got, asciiRatioMin)
	}
	if got := c2.CJKCharsPerToken(); got < cjkRatioMin {
		t.Errorf("CJK ratio = %v, below min %v", got, cjkRatioMin)
	}
}

func TestTokenCalibrator_Reset(t *testing.T) {
	c := NewTokenCalibrator()
	// Push past warmup and trigger adjustment
	for i := 0; i < 8; i++ {
		c.RecordSample(1000, 1200)
	}
	// Ratio should have changed
	oldRatio := c.ASCIICharsPerToken()
	if oldRatio == 3.5 {
		t.Fatal("expected ratio to change before reset")
	}
	c.Reset()
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("after reset, ASCII ratio = %v, want 3.5", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.5 {
		t.Errorf("after reset, CJK ratio = %v, want 1.5", got)
	}
}
