package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #4861: guided IM onboarding wizard - overview table, per-platform
// steps with anti-confusion notes, and the full status registry.

func zz4861Config() *config.Config {
	cfg := &config.Config{}
	cfg.IM.Adapters = map[string]config.IMAdapterConfig{
		"slack":   {Enabled: true, Platform: "slack"},
		"tg-bot":  {Enabled: false, Platform: "telegram"},
		"custom1": {Enabled: true, Platform: ""},
	}
	return cfg
}

func TestIssue4861_OnboardOverviewListsAllPlatforms(t *testing.T) {
	out := renderIMOnboardOverview(zz4861Config())
	for _, want := range []string{"Slack", "Telegram", "QQ", "Feishu", "connected", "configured, disabled", "not set up", "/im onboard qq", "/im onboard telegram"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview missing %q:\n%s", want, out)
		}
	}
	// Slack enabled -> connected; tg-bot disabled -> configured, disabled.
	if !strings.Contains(out, "Slack        connected") {
		t.Errorf("slack must read connected:\n%s", out)
	}
}

func TestIssue4861_OnboardStepsWithNotes(t *testing.T) {
	out := renderIMOnboardSteps(zz4861Config(), "telegram")
	for _, want := range []string{"Telegram", "1.", "BotFather", "NUMERIC user IDs", "NOT @usernames", "/tg", "(configured but disabled)"} {
		if !strings.Contains(out, want) {
			t.Errorf("telegram steps missing %q:\n%s", want, out)
		}
	}
	// Unknown platform gets a pointer back to the overview.
	if out := renderIMOnboardSteps(nil, "nope"); !strings.Contains(out, "Unknown platform") {
		t.Errorf("unknown platform must be rejected:\n%s", out)
	}
}

func TestIssue4861_StatusRegistryIncludesNotConfigured(t *testing.T) {
	out := renderIMStatusRegistry(zz4861Config())
	for _, want := range []string{"IM status registry", "Slack", "enabled", "slack", "Telegram", "configured, disabled", "tg-bot", "Discord", "not configured"} {
		if !strings.Contains(out, want) {
			t.Errorf("registry missing %q:\n%s", want, out)
		}
	}
	// 12 registry platforms must all be listed.
	if got := strings.Count(out, "\n  "); got < 12 {
		t.Errorf("registry must list all 12 platforms, got %d rows:\n%s", got, out)
	}
}

func TestIssue4861_PlatformMatchPrefixes(t *testing.T) {
	// Adapter names like "tg-bot"/"qq2"/"matrix_" must match their platform.
	cases := []struct {
		key, adapterName string
		want             bool
	}{
		{"telegram", "tg-bot", true},
		{"telegram", "telegram", true},
		{"telegram", "slack", false},
		{"qq", "qq2", true},
		{"matrix", "matrix_", true},
		{"qq", "qqq", false}, // prefix must end at separator/digit
	}
	for _, tc := range cases {
		cfg := &config.Config{}
		cfg.IM.Adapters = map[string]config.IMAdapterConfig{
			tc.adapterName: {Enabled: true},
		}
		if got, _ := imPlatformConfigured(cfg, tc.key); got != tc.want {
			t.Errorf("imPlatformConfigured(key=%q, adapter=%q) = %v, want %v", tc.key, tc.adapterName, got, tc.want)
		}
	}
}
