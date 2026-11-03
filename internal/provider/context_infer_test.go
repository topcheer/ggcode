package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

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
		{2_500_000, 2_000_000},
		{2_000_000, 2_000_000},
		{1_999_999, 1_000_000},
		{1_500_000, 1_000_000},
		{600_000, 512_000},
		{300_000, 256_000},
		{210_000, 200_000},
		{180_000, 168_000},
		{130_000, 128_000},
		{100_000, 0},
		{10_000, 0},
		{0, 0},
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

	negatives := []string{
		"request too large: input token count exceeds model limit",
		"prompt is too long",
		"context_length_exceeded",
		"rate limit reached: limit: 60000 requests per minute",
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

	err := fmt.Errorf("this model's maximum context length is 128000 tokens")
	var setMax int
	result := InferContextWindowFromError(
		err,
		130_000,
		200_000,
		"vendor|https://api.example.com|model-x",
		func(n int) { setMax = n },
	)
	if result != 128_000 {
		t.Errorf("expected 128000, got %d", result)
	}
	if setMax != 128_000 {
		t.Errorf("expected setMaxTokens called with 128000, got %d", setMax)
	}

	if got := LookupProbeCache("vendor|https://api.example.com|model-x"); got != 128_000 {
		t.Errorf("probe cache = %d, want 128000", got)
	}
}

func TestInferContextWindowFromError_ExactValueNormalizesToTier(t *testing.T) {
	resetProbeCacheForTest(t)

	err := errors.New("maximum is 200019")
	var setMax int
	result := InferContextWindowFromError(err, 210_000, 256_000, "k", func(n int) { setMax = n })
	if result != 200_000 || setMax != 200_000 {
		t.Errorf("expected 200000, got result=%d setMax=%d", result, setMax)
	}
}

func TestInferContextWindowFromError_SubTierExactTrusted(t *testing.T) {
	resetProbeCacheForTest(t)

	err := errors.New("maximum is 65536")
	var setMax int
	result := InferContextWindowFromError(err, 50_000, 128_000, "k", func(n int) { setMax = n })
	if result != 65_536 || setMax != 65_536 {
		t.Errorf("expected 65536, got result=%d setMax=%d", result, setMax)
	}
}

func TestInferContextWindowFromError_NoExactValueRefuses(t *testing.T) {
	resetProbeCacheForTest(t)

	err := errors.New("request too large: input token count exceeds model limit")
	result := InferContextWindowFromError(
		err,
		50_000,
		200_000,
		"vendor|url|model",
		func(n int) { t.Errorf("setMaxTokens called with %d; must not guess", n) },
	)
	if result != 0 {
		t.Errorf("expected 0 (refuse to guess), got %d", result)
	}

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
		"",
		func(n int) { t.Error("should not call setMaxTokens") },
	)
	if result != 0 {
		t.Errorf("expected 0 (empty key), got %d", result)
	}
}

func TestInferContextWindowFromError_MultipleOverflows(t *testing.T) {
	resetProbeCacheForTest(t)

	step := func(msg string, currentMax int, want int) {
		t.Helper()
		result := InferContextWindowFromError(
			errors.New(msg),
			300_000,
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
