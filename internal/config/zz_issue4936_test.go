package config

import "testing"
import "strings"

func TestDeepSeekV4SiblingMaxOutputSynced(t *testing.T) {
	cases := map[string]int{

		"@cf/deepseek-ai/deepseek-v4-flash-0731": 393216,

		// #5141 Class 2: official cap per api-docs.deepseek.com is
		// "1 and 384K (393216)"; the generator clamps every deepseek
		// family spelling to that value (was 384000 before the clamp).
		"deepseek-v4-flash:0731":          393216,
		"deepseek-v4-pro:0813":            393216,
		"deepseek-v4-pro@eu":              393216,
		"deepseek/deepseek-v4-flash-0731": 393216,
	}
	for model, want := range cases {
		cap, ok := lookupModelCapability(model)
		if !ok {
			// Upstream (models.dev) adds/removes sibling spellings week to
			// week; a missing spelling is presence drift, not a cap mismatch.
			// Assert only the spellings that currently exist (#5141 Class 2).
			t.Logf("lookupModelCapability(%q): no table entry (upstream presence drift) - skipping", model)
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

// TestWeeklyCalibrationW47 pins the W47 (2026-11-16) calibration set so a
// models.dev regen cannot silently revert them:
//   - Claude 4.6+ dotted aliases are 1M/128K per
//     https://platform.claude.com/docs/en/models/overview (were stale 200K rows)
//   - Claude Mythos 5.1/5.2 added (1M/128K, vision)
//   - google/gemini-4-* context is 1048576 (models.dev + OpenRouter agree);
//     output aligned to the committed 65536 family convention
//   - qwen4-max-preview: 1M context / 393216 shared output budget
//     https://help.aliyun.com/zh/model-studio/qwen4-max-preview
//   - gpt-6.1-sol-pro / gpt-6.1-terra-pro mirror the 6.1 family (1.05M/128K)
func TestWeeklyCalibrationW47(t *testing.T) {
	pins := map[string]modelCapability{
		"claude-opus-4.7":          {1000000, 128000, true},
		"claude-opus-4.8":          {1000000, 128000, true},
		"claude-sonnet-4.6":        {1000000, 128000, true},
		"claude-mythos-5.1":        {1000000, 128000, true},
		"claude-mythos-5.2":        {1000000, 128000, true},
		"google/gemini-4-argon":    {1048576, 65536, true},
		"google/gemini-4-carbon":   {1048576, 65536, true},
		"google/gemini-4-hydrogen": {1048576, 65536, true},
		"gpt-6.1-sol-pro":          {1050000, 128000, true},
		"gpt-6.1-terra-pro":        {1050000, 128000, true},
		"openai/gpt-6.1-sol-pro":   {1050000, 128000, true},
		"openai/gpt-6.1-terra-pro": {1050000, 128000, true},
		"qwen4-max-preview":        {1000000, 393216, true},
	}
	for model, want := range pins {
		got, ok := lookupModelCapability(model)
		if !ok {
			t.Errorf("lookupModelCapability(%q): no table entry (W47 calibration row lost)", model)
			continue
		}
		if got != want {
			t.Errorf("knownModelCapabilities[%q] = %+v, want %+v (W47)", model, got, want)
		}
	}
}
