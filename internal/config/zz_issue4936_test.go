package config

import "testing"
import "strings"

func TestDeepSeekV4SiblingMaxOutputSynced(t *testing.T) {
	cases := map[string]int{

		"@cf/deepseek-ai/deepseek-v4-flash-0731": 393216,

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

func TestDeepSeekV4NoEntryExceedsOfficialCap(t *testing.T) {
	for model, cap := range knownModelCapabilities {
		if cap.MaxOutputTokens > 393216 && strings.Contains(model, "deepseek-v4") {
			t.Errorf("knownModelCapabilities[%q].MaxOutputTokens = %d exceeds official 384K cap (#4936)", model, cap.MaxOutputTokens)
		}
	}
}

func TestRouterAliasCapsMirrorOfficial(t *testing.T) {
	pins := map[string][2]int{
		"~z-ai/glm-latest":       {1048576, 131072},
		"~z-ai/glm-flash-latest": {1048576, 131072},

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

	for model, cap := range knownModelCapabilities {
		if strings.HasPrefix(model, "~") && strings.Contains(model, "deepseek") && cap.MaxOutputTokens > 393216 {
			t.Errorf("router alias %q MaxOutputTokens=%d exceeds deepseek official cap (#4946)", model, cap.MaxOutputTokens)
		}
		if strings.HasPrefix(model, "~") && strings.Contains(model, "glm") && cap.MaxOutputTokens > 131072 {
			t.Errorf("router alias %q MaxOutputTokens=%d exceeds GLM official cap (#4946)", model, cap.MaxOutputTokens)
		}
	}
}
