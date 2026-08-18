package wailskit

// Issue #690: the #585 platform-switch guard only covered Extra. On a
// platform switch the transport-layer fields (Transport/Command/Args/Env,
// plus AllowFrom) were inherited unconditionally, so switching e.g.
// telegram→discord kept the old stdio bridge command and its env
// (TELEGRAM_BOT_TOKEN) — the new adapter would launch the OLD platform's
// bridge process with the OLD platform's credentials.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue690_PlatformSwitchResetsTransportFields(t *testing.T) {
	existing := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  "telegram",
		Transport: "stdio",
		Command:   "/usr/local/bin/tg-bridge",
		Args:      []string{"--listen", "tg"},
		Env:       map[string]string{"TELEGRAM_BOT_TOKEN": "secret-tg"},
		AllowFrom: []string{"@telegram-admin"},
	}
	update := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "discord",
	}

	got := mergeExistingIntoUpdate(update, existing)

	if got.Transport != "" {
		t.Errorf("Transport inherited across platform switch: %q", got.Transport)
	}
	if got.Command != "" {
		t.Errorf("Command inherited across platform switch: %q (old platform's bridge binary would be launched)", got.Command)
	}
	if len(got.Args) != 0 {
		t.Errorf("Args inherited across platform switch: %v", got.Args)
	}
	if len(got.Env) != 0 {
		t.Errorf("Env inherited across platform switch (credential leak): %v", got.Env)
	}
	if len(got.AllowFrom) != 0 {
		t.Errorf("AllowFrom inherited across platform switch: %v", got.AllowFrom)
	}
}

func TestIssue690_SamePlatformStillInheritsTransportFields(t *testing.T) {
	existing := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  "telegram",
		Transport: "stdio",
		Command:   "/usr/local/bin/tg-bridge",
		Args:      []string{"--listen", "tg"},
		Env:       map[string]string{"TELEGRAM_BOT_TOKEN": "secret-tg"},
		AllowFrom: []string{"@telegram-admin"},
	}
	update := config.IMAdapterConfig{
		Enabled:  true,
		Platform: "telegram", // same platform — inheritance must be preserved
	}

	got := mergeExistingIntoUpdate(update, existing)

	if got.Transport != "stdio" {
		t.Errorf("same-platform Transport = %q, want stdio", got.Transport)
	}
	if got.Command != "/usr/local/bin/tg-bridge" {
		t.Errorf("same-platform Command = %q, want /usr/local/bin/tg-bridge", got.Command)
	}
	if len(got.Args) != 2 || got.Args[0] != "--listen" {
		t.Errorf("same-platform Args = %v, want [--listen tg]", got.Args)
	}
	if got.Env["TELEGRAM_BOT_TOKEN"] != "secret-tg" {
		t.Errorf("same-platform Env lost TELEGRAM_BOT_TOKEN")
	}
	if len(got.AllowFrom) != 1 || got.AllowFrom[0] != "@telegram-admin" {
		t.Errorf("same-platform AllowFrom = %v, want [@telegram-admin]", got.AllowFrom)
	}
}
