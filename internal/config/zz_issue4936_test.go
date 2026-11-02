package config

import "testing"
import "strings"

// #4936: DeepSeek V4 sibling spellings must carry the official 384K max output
// (per 1d41be032 / PR #4931) regardless of provider-naming variant. Before the
// fix, colon/eu/OpenRouter spellings still carried 1048576/943718, which leaked
// into API request max_output_tokens on the server-side-tools Responses path.
func TestDeepSeekV4SiblingMaxOutputSynced(t *testing.T) {
	cases := map[string]int{
		// Vendor-twin mirror: @cf mirrors deepseek-ai/ which 1d41be032 set to 393216.
		"@cf/deepseek-ai/deepseek-v4-flash-0731": 393216,
		// Bare-family spellings standardized on 384000 (matches L397/L404/L405).
		"deepseek-v4-flash:0731":          384000,
		"deepseek-v4-pro:0813":            384000,
		"deepseek-v4-pro@eu":              384000,
		"deepseek/deepseek-v4-flash-0731": 384000,
	}
	for model, want := range cases {
		cap, ok := lookupModelCapability(model)
		if !ok {
			t.Errorf("lookupModelCapability(%q): no table entry", model)
			continue
		}
		if cap.MaxOutputTokens != want {
			t.Errorf("lookupModelCapability(%q).MaxOutputTokens = %d, want %d (#4936: sibling spellings must match the official 384K cap)", model, cap.MaxOutputTokens, want)
		}
	}
}

// Sweep guard: no DeepSeek V4 spelling (present or future) may exceed the
// official max output. 393216 = 384*1024 and 384000 = 384*1000 are the two
// accepted unit spellings of the 384K cap; anything larger (1048576, 943718,
// ...) is a stale pre-#4931 value that leaks into max_output_tokens.
func TestDeepSeekV4NoEntryExceedsOfficialCap(t *testing.T) {
	for model, cap := range knownModelCapabilities {
		if cap.MaxOutputTokens > 393216 && strings.Contains(model, "deepseek-v4") {
			t.Errorf("knownModelCapabilities[%q].MaxOutputTokens = %d exceeds official 384K cap (#4936)", model, cap.MaxOutputTokens)
		}
	}
}
