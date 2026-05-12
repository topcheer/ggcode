package provider

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

func TestMatchOverflowTier(t *testing.T) {
	tests := []struct {
		tokenCount int
		want       int
	}{
		{2_500_000, 2_000_000}, // above max tier → 2M
		{2_000_000, 2_000_000}, // exactly 2M → 2M
		{1_999_999, 1_000_000}, // just below 2M → 1M
		{1_500_000, 1_000_000}, // between 1M and 2M → 1M
		{600_000, 512_000},     // between 512K and 1M → 512K
		{300_000, 256_000},     // between 256K and 512K → 256K
		{210_000, 200_000},     // between 200K and 256K → 200K
		{180_000, 168_000},     // between 168K and 200K → 168K
		{130_000, 128_000},     // between 128K and 168K → 128K
		{100_000, 64_000},      // between 64K and 128K → 64K
		{10_000, 64_000},       // below all tiers → minimum (64K)
		{0, 64_000},            // zero → minimum
	}
	for _, tt := range tests {
		got := matchOverflowTier(tt.tokenCount)
		if got != tt.want {
			t.Errorf("matchOverflowTier(%d) = %d, want %d", tt.tokenCount, got, tt.want)
		}
	}
}

func TestInferContextWindowFromError_WithExactValue(t *testing.T) {
	// Simulate an error message that includes the exact context window limit.
	err := fmt.Errorf("this model's maximum context length is 128000 tokens")
	var setMax int
	result := InferContextWindowFromError(
		err,
		130_000, // currentTokenCount
		200_000, // currentMaxTokens
		"vendor|https://api.example.com|model-x",
		func(n int) { setMax = n },
	)
	if result != 128_000 {
		t.Errorf("expected 128000, got %d", result)
	}
	if setMax != 128_000 {
		t.Errorf("expected setMaxTokens called with 128000, got %d", setMax)
	}
}

func TestInferContextWindowFromError_WithEstimate(t *testing.T) {
	// Error without exact value — should use currentTokenCount to match tier.
	err := errors.New("request too large: input token count exceeds model limit")
	var setMax int
	result := InferContextWindowFromError(
		err,
		270_000, // currentTokenCount → should match to 256K tier
		512_000, // currentMaxTokens
		"vendor|https://api.example.com|model-y",
		func(n int) { setMax = n },
	)
	if result != 256_000 {
		t.Errorf("expected 256000, got %d", result)
	}
	if setMax != 256_000 {
		t.Errorf("expected setMaxTokens called with 256000, got %d", setMax)
	}
}

func TestInferContextWindowFromError_NoUpdateNeeded(t *testing.T) {
	// Inferred tier >= currentMaxTokens → no update.
	err := errors.New("request too large")
	result := InferContextWindowFromError(
		err,
		300_000, // currentTokenCount → 256K tier
		128_000, // currentMaxTokens is already 128K (below 256K)
		"vendor|url|model",
		func(n int) { t.Error("should not call setMaxTokens") },
	)
	if result != 0 {
		t.Errorf("expected 0 (no update), got %d", result)
	}
}

func TestInferContextWindowFromError_EmptyProbeKey(t *testing.T) {
	err := errors.New("context length exceeded 200000")
	result := InferContextWindowFromError(
		err,
		300_000,
		512_000,
		"", // empty probe key → no-op
		func(n int) { t.Error("should not call setMaxTokens") },
	)
	if result != 0 {
		t.Errorf("expected 0 (empty key), got %d", result)
	}
}

func TestInferContextWindowFromError_MultipleOverflows(t *testing.T) {
	// Simulate progressive overflow: first request with 270K tokens (→ 256K),
	// then 240K tokens (→ 200K). Each overflow should further reduce the tier.
	err := errors.New("input token count exceeds model limit")
	var setMax atomic.Int32

	// First overflow: 270K tokens, current max is 512K → should infer 256K
	result := InferContextWindowFromError(
		err,
		270_000,
		512_000,
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 256_000 {
		t.Errorf("first overflow: expected 256000, got %d", result)
	}
	if setMax.Load() != 256000 {
		t.Errorf("first overflow setMax: expected 256000, got %d", setMax.Load())
	}

	// Second overflow: 240K tokens, current max is now 256K → should infer 200K
	result = InferContextWindowFromError(
		err,
		240_000,
		256_000, // reduced from first inference
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 200_000 {
		t.Errorf("second overflow: expected 200000, got %d", result)
	}
	if setMax.Load() != 200000 {
		t.Errorf("second overflow setMax: expected 200000, got %d", setMax.Load())
	}

	// Third overflow: 190K tokens, current max is 200K → 168K < 200K, still reduces
	result = InferContextWindowFromError(
		err,
		190_000,
		200_000,
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 168_000 {
		t.Errorf("third overflow: expected 168000, got %d", result)
	}
	if setMax.Load() != 168000 {
		t.Errorf("third overflow setMax: expected 168000, got %d", setMax.Load())
	}

	// Fourth overflow: 150K tokens, current max is now 168K → 128K < 168K, reduces
	result = InferContextWindowFromError(
		err,
		150_000,
		168_000,
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 128_000 {
		t.Errorf("fourth overflow: expected 128000, got %d", result)
	}

	// Stable state: 100K tokens, current max is now 128K → 64K < 128K
	// But this means 128K was still too high. One more step to 64K.
	result = InferContextWindowFromError(
		err,
		100_000,
		128_000,
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 64_000 {
		t.Errorf("fifth overflow: expected 64000, got %d", result)
	}

	// No further reduction: even 64K overflows → minimum is 64K, no smaller tier
	result = InferContextWindowFromError(
		err,
		50_000,
		64_000,
		"vendor|url|model",
		func(n int) { setMax.Store(int32(n)) },
	)
	if result != 0 {
		t.Errorf("sixth overflow: expected 0 (cannot reduce further), got %d", result)
	}
}
