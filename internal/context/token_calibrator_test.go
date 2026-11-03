package context

import (
	"github.com/topcheer/ggcode/internal/provider"
	"testing"
)

func TestTokenCalibrator_DefaultRatios(t *testing.T) {
	c := NewTokenCalibrator()
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("default ASCII ratio = %v, want 3.5", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("default CJK ratio = %v, want 1.0 (#515)", got)
	}
}

func TestTokenCalibrator_RecordSample_AdjustsRatio(t *testing.T) {
	c := NewTokenCalibrator()
	for i := 0; i < 5; i++ {
		c.RecordSample(1000, 1200, 4000, 0, 0)
	}
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Fatalf("after warmup, ASCII ratio = %v, want 3.5", got)
	}
	for i := 0; i < 3; i++ {
		c.RecordSample(1000, 1200, 4000, 0, 0)
	}
	if got := c.ASCIICharsPerToken(); got >= 3.5 {
		t.Errorf("after calibration, ASCII ratio = %v, should be < 3.5 (was underestimating)", got)
	}
}

func TestTokenCalibrator_WarmupNoAdjust(t *testing.T) {
	c := NewTokenCalibrator()
	for i := 0; i < calibWarmupSamples; i++ {
		c.RecordSample(500, 2000, 4000, 0, 0)
	}
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("during warmup, ASCII ratio = %v, want 3.5 (no adjustment)", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("during warmup, CJK ratio = %v, want 1.0 (no adjustment)", got)
	}
}

func TestTokenCalibrator_RatioClamped(t *testing.T) {
	c := NewTokenCalibrator()
	for i := 0; i < calibWarmupSamples; i++ {
		c.RecordSample(100, 1, 4000, 0, 0)
	}
	for i := 0; i < 30; i++ {
		c.RecordSample(100, 1, 4000, 0, 0)
	}
	if got := c.ASCIICharsPerToken(); got > asciiRatioMax {
		t.Errorf("ASCII ratio = %v, exceeds max %v", got, asciiRatioMax)
	}
	if got := c.CJKCharsPerToken(); got > cjkRatioMax {
		t.Errorf("CJK ratio = %v, exceeds max %v", got, cjkRatioMax)
	}
	c2 := NewTokenCalibrator()
	for i := 0; i < calibWarmupSamples; i++ {
		c2.RecordSample(1, 10000, 4000, 0, 0)
	}
	for i := 0; i < 30; i++ {
		c2.RecordSample(1, 10000, 4000, 0, 0)
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
	for i := 0; i < 8; i++ {
		c.RecordSample(1000, 1200, 4000, 0, 0)
	}
	oldRatio := c.ASCIICharsPerToken()
	if oldRatio == 3.5 {
		t.Fatal("expected ratio to change before reset")
	}
	c.Reset()
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("after reset, ASCII ratio = %v, want 3.5", got)
	}
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("after reset, CJK ratio = %v, want 1.0", got)
	}
}

func TestTokenCalibrator_CompositionIsolation(t *testing.T) {
	c := NewTokenCalibrator()
	for i := 0; i < 11; i++ {
		c.RecordSample(2000, 1000, 8000, 0, 0)
	}
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("pure-ASCII samples must not move cjkRatio: got %v, want 1.0 (default, unobserved)", got)
	}
	if got := c.ASCIICharsPerToken(); got <= 3.5 {
		t.Errorf("asciiRatio should have adjusted upward: got %v, want > 3.5", got)
	}

	c2 := NewTokenCalibrator()
	for i := 0; i < 11; i++ {
		c2.RecordSample(2000, 1000, 0, 3000, 0)
	}
	if got := c2.ASCIICharsPerToken(); got != 3.5 {
		t.Errorf("pure-CJK samples must not move asciiRatio: got %v, want 3.5", got)
	}
	if got := c2.CJKCharsPerToken(); got <= 1.0 {
		t.Errorf("cjkRatio should have adjusted upward: got %v, want > 1.0", got)
	}
}

func TestTokenCalibrator_MixedSampleSplitsFactor(t *testing.T) {
	pure := NewTokenCalibrator()
	mixed := NewTokenCalibrator()
	for i := 0; i < 8; i++ {
		pure.RecordSample(1250, 1000, 8000, 0, 0)
		mixed.RecordSample(1250, 1000, 4000, 3000, 0)
	}
	pureA := pure.ASCIICharsPerToken()
	mixedA := mixed.ASCIICharsPerToken()
	mixedC := mixed.CJKCharsPerToken()
	if mixedA <= 3.5 || mixedC <= 1.0 {
		t.Errorf("mixed sample should adjust both ratios upward: ascii=%v cjk=%v", mixedA, mixedC)
	}
	if mixedA >= pureA {
		t.Errorf("mixed ascii adjustment (%v) should be weaker than pure (%v)", mixedA, pureA)
	}
}

func TestManagerCompositionLocked(t *testing.T) {
	m := Manager{messages: []provider.Message{
		{Content: []provider.ContentBlock{{Text: "hello world"}}},
		{Content: []provider.ContentBlock{{Text: "你好，世界"}}},
		{Content: []provider.ContentBlock{{Text: "mixed 你好 ok"}}},
		{Content: []provider.ContentBlock{{Text: "café"}}},
		{Content: []provider.ContentBlock{{Output: "tool output 12"}}},
		{Content: []provider.ContentBlock{{ReasoningContent: "think 45"}}},
	}}
	a, c, le := m.compositionLocked()
	if a != 45 {
		t.Errorf("asciiChars = %d, want %d", a, 45)
	}
	if c != 6 {
		t.Errorf("cjkChars = %d, want %d", c, 6)
	}
	if le != 1 {
		t.Errorf("latinExtChars = %d, want 1", le)
	}
}
