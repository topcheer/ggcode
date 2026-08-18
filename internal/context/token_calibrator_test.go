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
	// Feed warmup samples (should not adjust)
	for i := 0; i < 5; i++ {
		c.RecordSample(1000, 1200, 4000, 0, 0) // estimated 1000, actual 1200
	}
	// Ratio should still be default after warmup
	if got := c.ASCIICharsPerToken(); got != 3.5 {
		t.Fatalf("after warmup, ASCII ratio = %v, want 3.5", got)
	}
	// Feed 3 more samples to trigger first adjustment (sample 8)
	for i := 0; i < 3; i++ {
		c.RecordSample(1000, 1200, 4000, 0, 0)
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
		c.RecordSample(500, 2000, 4000, 0, 0) // extreme but should be ignored
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
	// Bypass warmup
	for i := 0; i < calibWarmupSamples; i++ {
		c.RecordSample(100, 1, 4000, 0, 0) // extreme: estimated >> actual
	}
	// Force several adjustments
	for i := 0; i < 30; i++ {
		c.RecordSample(100, 1, 4000, 0, 0) // factor = 100, wants to push ratio very high
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
		c2.RecordSample(1, 10000, 4000, 0, 0)
	}
	for i := 0; i < 30; i++ {
		c2.RecordSample(1, 10000, 4000, 0, 0) // factor = 0.0001, wants to push ratio very low
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
		c.RecordSample(1000, 1200, 4000, 0, 0)
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
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("after reset, CJK ratio = %v, want 1.0", got)
	}
}

// TestTokenCalibrator_CompositionIsolation (#355): a pure-ASCII sample must
// only adjust asciiRatio. Previously a single factor updated BOTH ratios, so
// 11 pure-ASCII samples pushed cjkRatio 1.5→2.0 (clamp) with CJK never
// observed — CJK estimates were then undercounted ~25%.
func TestTokenCalibrator_CompositionIsolation(t *testing.T) {
	c := NewTokenCalibrator()
	// 11 pure-ASCII samples with est=2×actual (strong upward pressure).
	for i := 0; i < 11; i++ {
		c.RecordSample(2000, 1000, 8000, 0, 0)
	}
	if got := c.CJKCharsPerToken(); got != 1.0 {
		t.Errorf("pure-ASCII samples must not move cjkRatio: got %v, want 1.0 (default, unobserved)", got)
	}
	if got := c.ASCIICharsPerToken(); got <= 3.5 {
		t.Errorf("asciiRatio should have adjusted upward: got %v, want > 3.5", got)
	}

	// Reverse: pure-CJK samples must not move asciiRatio.
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

// TestTokenCalibrator_MixedSampleSplitsFactor (#355): a 50/50 mixed sample
// adjusts BOTH ratios, each weighted by its composition share (half as
// strongly as a pure sample).
func TestTokenCalibrator_MixedSampleSplitsFactor(t *testing.T) {
	pure := NewTokenCalibrator()
	mixed := NewTokenCalibrator()
	for i := 0; i < 8; i++ {
		pure.RecordSample(1250, 1000, 8000, 0, 0)     // pure ASCII
		mixed.RecordSample(1250, 1000, 4000, 3000, 0) // ~57%/43% mix
	}
	pureA := pure.ASCIICharsPerToken()
	mixedA := mixed.ASCIICharsPerToken()
	mixedC := mixed.CJKCharsPerToken()
	if mixedA <= 3.5 || mixedC <= 1.0 {
		t.Errorf("mixed sample should adjust both ratios upward: ascii=%v cjk=%v", mixedA, mixedC)
	}
	// Mixed ASCII adjustment must be weaker than pure (share < 1).
	if mixedA >= pureA {
		t.Errorf("mixed ascii adjustment (%v) should be weaker than pure (%v)", mixedA, pureA)
	}
}

// TestManagerCompositionLocked (#355): message text composition counting.
func TestManagerCompositionLocked(t *testing.T) {
	m := Manager{messages: []provider.Message{
		{Content: []provider.ContentBlock{{Text: "hello world"}}},          // 11 ASCII
		{Content: []provider.ContentBlock{{Text: "你好，世界"}}},                // 5 CJK
		{Content: []provider.ContentBlock{{Text: "mixed 你好 ok"}}},          // 9 ASCII + 2 CJK
		{Content: []provider.ContentBlock{{Text: "café"}}},                 // non-CJK non-ASCII: ignored
		{Content: []provider.ContentBlock{{Output: "tool output 12"}}},     // output text counts
		{Content: []provider.ContentBlock{{ReasoningContent: "think 45"}}}, // reasoning counts
	}}
	a, c, le := m.compositionLocked()
	// ASCII: 11 (hello world) + 9 (mixed 你好 ok) + 3 (caf; é is Latin-Ext, returned separately per #634) + 14 (tool output 12) + 8 (think 45)
	if a != 45 {
		t.Errorf("asciiChars = %d, want %d", a, 45)
	}
	// CJK: 你好世界 (4; fullwidth ， U+FF0C not in ranges) + 你好 (2) = 6.
	// #598: é (Latin-Ext) no longer counts — Cyrillic/Greek/LatinExt were
	// removed from the CJK calibration bucket (2-3x token-density gap vs
	// CJK; their inclusion pegged cjkRatio to clamp and underestimated
	// Chinese ~50%).
	if c != 6 {
		t.Errorf("cjkChars = %d, want %d", c, 6)
	}
	if le != 1 { // café's é — covered-script count per #634
		t.Errorf("latinExtChars = %d, want 1", le)
	}
}
