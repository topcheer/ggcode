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

// #4946: the router-alias layer (~ prefix, e.g. OpenRouter vendor routes) was
// skipped by both the #4931 and #4937 calibration rounds because they matched
// concrete version keys. Router aliases must mirror their target model's
// official caps; before the fix they carried the pre-#4931 stale values
// (943718/1310720) that the concrete-key fixes were supposed to have cleared.
// Guard: any ~-prefixed alias naming deepseek (any generation) must not exceed
// the official 393216 output cap; GLM aliases must not exceed 131072.
func TestRouterAliasCapsMirrorOfficial(t *testing.T) {
	pins := map[string][2]int{ // model -> {ContextWindow, MaxOutputTokens}
		"~z-ai/glm-latest":       {1048576, 131072},
		"~z-ai/glm-flash-latest": {1048576, 131072},
		// Kimi K3 official: 1M window, max_completion_tokens up to 1048576
		// (platform.kimi.ai quickstart, "Important limits").
		"~moonshotai/kimi-latest":         {1048576, 1048576},
		"~deepseek/deepseek-flash-latest": {1048576, 393216},
		"~deepseek/deepseek-pro-latest":   {1048576, 393216},
	}
	for model, want := range pins {
		cap, ok := lookupModelCapability(model)
		if !ok {
			t.Errorf("lookupModelCapability(%q): no table entry", model)
			continue
		}
		if cap.ContextWindow != want[0] || cap.MaxOutputTokens != want[1] {
			t.Errorf("knownModelCapabilities[%q] = {win %d, out %d}, want {win %d, out %d} (#4946: router alias must mirror official caps)", model, cap.ContextWindow, cap.MaxOutputTokens, want[0], want[1])
		}
	}
	// Sweep: no router alias for deepseek (any spelling) above the 384K cap.
	for model, cap := range knownModelCapabilities {
		if strings.HasPrefix(model, "~") && strings.Contains(model, "deepseek") && cap.MaxOutputTokens > 393216 {
			t.Errorf("router alias %q MaxOutputTokens=%d exceeds deepseek official cap (#4946)", model, cap.MaxOutputTokens)
		}
		if strings.HasPrefix(model, "~") && strings.Contains(model, "glm") && cap.MaxOutputTokens > 131072 {
			t.Errorf("router alias %q MaxOutputTokens=%d exceeds GLM official cap (#4946)", model, cap.MaxOutputTokens)
		}
	}
}
