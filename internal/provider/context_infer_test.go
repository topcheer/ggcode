package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// resetProbeCacheForTest isolates the probe cache for the test: no auto-load
// from the real config dir, and writes go to a temp HOME (set via t.Setenv).
func resetProbeCacheForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	probeCacheMu.Lock()
	probeCache = map[string]int{}
	probeLoaded = true
	probeCacheMu.Unlock()
}

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
		{100_000, 0},           // below all tiers → NO tier: never invent a floor
		{10_000, 0},            // below all tiers → 0
		{0, 0},                 // zero → 0
	}
	for _, tt := range tests {
		got := matchOverflowTier(tt.tokenCount)
		if got != tt.want {
			t.Errorf("matchOverflowTier(%d) = %d, want %d", tt.tokenCount, got, tt.want)
		}
	}
}

func TestParseContextWindowFromError(t *testing.T) {
	positives := []struct {
		msg  string
		want int
	}{
		{"this model's maximum context length is 128000 tokens", 128000},
		{"prompt is too long: 40123 tokens > 200000 tokens maximum", 200000},
		{"requested 40123 tokens, maximum is 131072", 131072},
		{"input token count exceeds limits: (200000 tokens)", 200000},
		{"Token limit: 131072 reached", 131072},
		{"context limit of 8192 tokens exceeded", 8192},
		{"maximum of 1048576 tokens allowed", 1048576},
		{"model context max 1000000 tokens", 1000000},
	}
	for _, tt := range positives {
		if got := parseContextWindowFromError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("parse(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}

	// Negative cases: overflow-style or unrelated errors WITHOUT a precise
	// context limit must parse to 0 — they must never feed window inference.
	negatives := []string{
		"request too large: input token count exceeds model limit",
		"prompt is too long",
		"context_length_exceeded",
		"rate limit reached: limit: 60000 requests per minute", // rate, not context
		"your request is too large",
		"too many tokens",
	}
	for _, msg := range negatives {
		if got := parseContextWindowFromError(errors.New(msg)); got != 0 {
			t.Errorf("parse(%q) = %d, want 0 (must not parse)", msg, got)
		}
	}
}

func TestInferContextWindowFromError_WithExactValue(t *testing.T) {
	resetProbeCacheForTest(t)

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
	// Persisted value normalizes to the tier and is reusable.
	if got := LookupProbeCache("vendor|https://api.example.com|model-x"); got != 128_000 {
		t.Errorf("probe cache = %d, want 128000", got)
	}
}

func TestInferContextWindowFromError_ExactValueNormalizesToTier(t *testing.T) {
	resetProbeCacheForTest(t)

	// A precise provider number like 200019 (Gemini) snaps down to the 200K
	// tier rather than propagating an odd value.
	err := errors.New("maximum is 200019")
	var setMax int
	result := InferContextWindowFromError(err, 210_000, 256_000, "k", func(n int) { setMax = n })
	if result != 200_000 || setMax != 200_000 {
		t.Errorf("expected 200000, got result=%d setMax=%d", result, setMax)
	}
}

func TestInferContextWindowFromError_SubTierExactTrusted(t *testing.T) {
	resetProbeCacheForTest(t)

	// A sub-128K model reporting its precise limit (65536) is trusted as-is;
	// it must NOT be snapped up to the 128K minimum tier.
	err := errors.New("maximum is 65536")
	var setMax int
	result := InferContextWindowFromError(err, 50_000, 128_000, "k", func(n int) { setMax = n })
	if result != 65_536 || setMax != 65_536 {
		t.Errorf("expected 65536, got result=%d setMax=%d", result, setMax)
	}
}

func TestInferContextWindowFromError_NoExactValueRefuses(t *testing.T) {
	resetProbeCacheForTest(t)

	// Regression: an overflow error WITHOUT a parseable number must never
	// touch the context window. The old code fell back to matching the local
	// token estimate against tiers — which shrank the window to the 64K
	// minimum tier whenever the estimate undercounted, persisted that value,
	// and left the session stuck at 64K.
	err := errors.New("request too large: input token count exceeds model limit")
	result := InferContextWindowFromError(
		err,
		50_000, // small local estimate: old code matched the 64K floor
		200_000,
		"vendor|url|model",
		func(n int) { t.Errorf("setMaxTokens called with %d; must not guess", n) },
	)
	if result != 0 {
		t.Errorf("expected 0 (refuse to guess), got %d", result)
	}

	// Even a large estimate must not trigger a tier guess.
	err2 := errors.New("prompt is too long")
	result2 := InferContextWindowFromError(
		err2,
		270_000,
		512_000,
		"vendor|url|model",
		func(n int) { t.Errorf("setMaxTokens called with %d; must not guess", n) },
	)
	if result2 != 0 {
		t.Errorf("expected 0 (refuse to guess), got %d", result2)
	}

	if got := LookupProbeCache("vendor|url|model"); got != 0 {
		t.Errorf("probe cache = %d, want 0 (nothing persisted)", got)
	}
}

func TestInferContextWindowFromError_RateLimitDoesNotShrink(t *testing.T) {
	resetProbeCacheForTest(t)

	// Regression: a rate-limit error that slipped through the outer keyword
	// gate used to parse 60000 via "limit\W+(\d+)" and shrink a 128K window
	// to 64K. Parsing must require token-context wording.
	err := errors.New("Rate limit reached for tokens. Your limit: 60000 requests/min")
	result := InferContextWindowFromError(
		err,
		90_000,
		128_000,
		"vendor|url|model",
		func(n int) { t.Errorf("setMaxTokens called with %d; rate limit must not shrink window", n) },
	)
	if result != 0 {
		t.Errorf("expected 0, got %d", result)
	}
}

func TestInferContextWindowFromError_NoUpdateNeeded(t *testing.T) {
	resetProbeCacheForTest(t)

	// Exact value above current max → no reduction needed.
	err := errors.New("maximum is 131072")
	result := InferContextWindowFromError(
		err,
		120_000,
		128_000,
		"vendor|url|model",
		func(n int) { t.Error("should not call setMaxTokens") },
	)
	if result != 0 {
		t.Errorf("expected 0 (no update), got %d", result)
	}
}

func TestInferContextWindowFromError_EmptyProbeKey(t *testing.T) {
	resetProbeCacheForTest(t)

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
	resetProbeCacheForTest(t)

	// Progressive overflow with EXACT limits in every error. Each overflow
	// reduces the window to the reported limit, but only when the number is
	// precise.
	step := func(msg string, currentMax int, want int) {
		t.Helper()
		result := InferContextWindowFromError(
			errors.New(msg),
			300_000, // estimate is now irrelevant
			currentMax,
			"vendor|url|model",
			func(n int) {
				if n != want {
					t.Errorf("setMaxTokens = %d, want %d (msg=%q)", n, want, msg)
				}
			},
		)
		if result != want {
			t.Errorf("overflow %q: expected %d, got %d", msg, want, result)
		}
	}

	step("maximum is 262144", 512_000, 256_000)
	step("maximum is 204800", 256_000, 200_000)
	step("maximum is 131072", 200_000, 128_000)

	// At the 128K minimum tier, a numberless overflow changes nothing.
	result := InferContextWindowFromError(
		errors.New("prompt is too long"),
		50_000,
		128_000,
		"vendor|url|model",
		func(n int) { t.Error("must not shrink below the minimum tier by guessing") },
	)
	if result != 0 {
		t.Errorf("expected 0 at minimum tier, got %d", result)
	}
}

func TestLoadProbeCacheDropsLegacySub128K(t *testing.T) {
	// Regression: cache files written by the old estimate-based inference can
	// contain sub-128K values (usually the bogus 64000 floor). Loading must
	// drop them; healthy values pass through untouched.
	t.Setenv("HOME", t.TempDir())

	dir := filepath.Join(config.ConfigDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"a|u|legacy64k": 64000, "a|u|legacy100k": 100000, "a|u|claude": 200000, "a|u|gemini": 1000000}`
	if err := os.WriteFile(filepath.Join(dir, "context_windows.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	probeCacheMu.Lock()
	probeCache = nil
	probeLoaded = false
	probeCacheMu.Unlock()

	if got := LookupProbeCache("a|u|legacy64k"); got != 0 {
		t.Errorf("legacy 64K entry survived migration: %d", got)
	}
	if got := LookupProbeCache("a|u|legacy100k"); got != 0 {
		t.Errorf("legacy 100K entry survived migration: %d", got)
	}
	if got := LookupProbeCache("a|u|claude"); got != 200_000 {
		t.Errorf("claude entry = %d, want 200000", got)
	}
	if got := LookupProbeCache("a|u|gemini"); got != 1_000_000 {
		t.Errorf("gemini entry = %d, want 1000000", got)
	}
}
