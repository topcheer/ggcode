package config

import (
	"os"
	"strings"
	"testing"
)

// #5050: FindAll* is non-overlapping, so the old pattern's captured trailing
// boundary (the '-' after 8k) was also the next token's leading boundary and
// any later, larger token never matched: testmodel-8k-128k -> 8000 instead of
// 128000. The leading boundary is now enforced in code (start-of-string or
// non-digit before the match), so adjacent tokens both count and take-max wins.
func TestIssue5050ContextWindowHintAdjacentTokens(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"testmodel-8k-128k", 128000},
		{"glm-4-9k-200k", 200000},
		{"model-4k_64k", 64000},
		{"model-8k.128k", 128000},
		{"8k,128k", 128000},
		{"model-1m-100m-pro", 100000000},
		{"model-100k-32k", 100000},
		{"big-1m-small-8k", 1000000},
		{"8k-128k", 128000},
		{"huge-2m", 2000000},

		// Single tokens keep working (leading boundary at start-of-string).
		{"moonshot-v1-32k", 32000},
		{"my-model-256k", 256000},
		{"model-1m-turbo", 1000000},

		// Greedy digit run: "9128k" matches as a whole token. Pre-existing
		// behavior both before and after #5050 (old leading [^0-9] matched the
		// '-' before the run identically); pinned so the refactor provably
		// does not change single-run semantics.
		{"model-9128k-64k", 9128000},

		// Letters glued after the token reject the whole token (versioning),
		// and names without any hint fall through to the legacy family rules.
		{"model-8kb", 0},
		{"totally-unrelated-model", 0},
	}
	for _, tc := range cases {
		if got := parseContextWindowHint(tc.model); got != tc.want {
			t.Errorf("parseContextWindowHint(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

func TestIssue5050InferContextWindowMultiToken(t *testing.T) {
	// Full path: table miss -> hint wins over the Contains family rules.
	// testmodel contains no family keyword, glm-4 is not in the capability
	// table under this spelling, so the hint is authoritative in both cases.
	if got := inferContextWindow("testmodel-8k-128k", "openai"); got != 128000 {
		t.Errorf("inferContextWindow(\"testmodel-8k-128k\") = %d, want 128000", got)
	}
	if got := inferContextWindow("glm-4-9k-200k", "openai"); got != 200000 {
		t.Errorf("inferContextWindow(\"glm-4-9k-200k\") = %d, want 200000", got)
	}
}

// The hint parser lives in generated code: internal/config/context_window.go
// is emitted by scripts/sync-model-caps.go's staticCode() template. Guard both
// halves so neither a hand-edit divergence nor the next `make sync-model-caps`
// run can silently reintroduce the boundary-consuming pattern.
func TestIssue5050GeneratorTemplateCarriesFix(t *testing.T) {
	const (
		newPattern = "`(\\d+)(k|m)($|[^a-z0-9])`"
		oldPattern = "`(^|[^0-9])(\\d+)(k|m)($|[^a-z0-9])`"
		boundaryOK = "FindAllStringSubmatchIndex"
	)
	for _, path := range []string{
		"context_window.go",
		"../../scripts/sync-model-caps.go",
	} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("#5050 read %s: %v", path, err)
		}
		code := string(src)
		if !strings.Contains(code, newPattern) {
			t.Errorf("#5050 %s does not carry the zero-leading-boundary pattern", path)
		}
		if strings.Contains(code, oldPattern) {
			t.Errorf("#5050 %s still contains the boundary-consuming pattern", path)
		}
		if !strings.Contains(code, boundaryOK) {
			t.Errorf("#5050 %s lacks the %s-based in-code boundary check", path, boundaryOK)
		}
	}
}
