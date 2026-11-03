package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue2869_NonCanonicalExistingPlatformIsNotASwitch(t *testing.T) {
	existing := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  "Telegram",
		Transport: "stdio",
		Command:   "/usr/local/bin/tg-bridge",
		Args:      []string{"--listen", "tg"},
		Env:       map[string]string{"TELEGRAM_BOT_TOKEN": "secret-tg"},
		AllowFrom: []string{"@telegram-admin"},
		Extra: map[string]interface{}{
			"legacy_bot_token": "old-secret",
		},
	}

	update := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
		Extra: map[string]interface{}{
			"bot_token": "new-secret",
		},
	}

	got := mergeExistingIntoUpdate(update, existing)

	if got.Transport != "stdio" {
		t.Errorf("Transport = %q, want stdio (same platform, only other fields edited)", got.Transport)
	}
	if got.Command != "/usr/local/bin/tg-bridge" {
		t.Errorf("Command = %q, want /usr/local/bin/tg-bridge", got.Command)
	}
	if len(got.Args) != 2 || got.Args[0] != "--listen" {
		t.Errorf("Args = %v, want [--listen tg] preserved", got.Args)
	}
	if got.Env["TELEGRAM_BOT_TOKEN"] != "secret-tg" {
		t.Errorf("Env lost TELEGRAM_BOT_TOKEN (silent credential loss)")
	}
	if len(got.AllowFrom) != 1 || got.AllowFrom[0] != "@telegram-admin" {
		t.Errorf("AllowFrom = %v, want [@telegram-admin] preserved", got.AllowFrom)
	}
	if got.Extra["legacy_bot_token"] != "old-secret" {
		t.Errorf("old Extra key legacy_bot_token dropped: %v", got.Extra)
	}
	if got.Extra["bot_token"] != "new-secret" {
		t.Errorf("update's bot_token must win over old Extra value: %v", got.Extra)
	}
}

func TestIssue2869_PaddedExistingPlatformIsNotASwitch(t *testing.T) {
	existing := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  " Telegram ",
		Transport: "stdio",
	}
	update := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram",
	}

	got := mergeExistingIntoUpdate(update, existing)

	if got.Transport != "stdio" {
		t.Errorf("Transport = %q, want stdio (padded legacy platform is not a switch)", got.Transport)
	}
}

func TestIssue2869_RealPlatformSwitchStillResets(t *testing.T) {
	existing := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  "Telegram",
		Transport: "stdio",
		Command:   "/usr/local/bin/tg-bridge",
		Args:      []string{"--listen"},
		Env:       map[string]string{"TELEGRAM_BOT_TOKEN": "secret-tg"},
		AllowFrom: []string{"@telegram-admin"},
		Extra: map[string]interface{}{
			"legacy_bot_token": "old-secret",
		},
	}
	update := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "discord",
	}

	got := mergeExistingIntoUpdate(update, existing)

	if got.Transport != "" || got.Command != "" || len(got.Args) != 0 || len(got.Env) != 0 || len(got.AllowFrom) != 0 {
		t.Errorf("real switch must reset transport family, got %+v", got)
	}
	if len(got.Extra) != 0 {
		t.Errorf("real switch must drop old Extra (credential leak), got %v", got.Extra)
	}
}
