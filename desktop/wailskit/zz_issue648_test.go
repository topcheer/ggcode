//go:build goolm

package wailskit

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func setupIssue648Home(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.MkdirAll(tmpDir+"/.ggcode", 0755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)
}

func TestIssue648_PlatformNormalizedToCanonicalID(t *testing.T) {
	setupIssue648Home(t)

	err := SaveIMAdapter("tg", map[string]string{
		"platform":  "Telegram",
		"bot_token": "123456:ABC-DEF",
	})
	if err != nil {
		t.Fatalf("saving with DisplayName casing must succeed: %v", err)
	}

	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.IM.Adapters["tg"]
	if !ok {
		t.Fatal("adapter not persisted")
	}
	if got.Platform != "telegram" {
		t.Fatalf("#648: platform persisted as %q, want canonical %q — runtime exact-match switch will silently skip this adapter", got.Platform, "telegram")
	}
}

func TestIssue648_CanonicalStaysCanonical(t *testing.T) {
	setupIssue648Home(t)

	if err := SaveIMAdapter("a1", map[string]string{"platform": "telegram", "bot_token": "t"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveIMAdapter("a2", map[string]string{"platform": "DisCord", "token": "t"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.IM.Adapters["a1"].Platform; p != "telegram" {
		t.Fatalf("canonical input mangled: %q", p)
	}
	if p := cfg.IM.Adapters["a2"].Platform; p != "discord" {
		t.Fatalf("mixed-case not normalized: %q", p)
	}
}

func TestIssue648_UpdatePathAlsoNormalizes(t *testing.T) {
	setupIssue648Home(t)

	if err := SaveIMAdapter("upd", map[string]string{"platform": "telegram", "bot_token": "t1"}); err != nil {
		t.Fatal(err)
	}

	if err := SaveIMAdapter("upd", map[string]string{"platform": "Telegram", "bot_token": "t2"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.IM.Adapters["upd"].Platform; p != "telegram" {
		t.Fatalf("#648 update path: platform persisted as %q, want %q", p, "telegram")
	}
}

func TestIssue648_UnknownStillRejected(t *testing.T) {
	setupIssue648Home(t)
	err := SaveIMAdapter("bad", map[string]string{"platform": "telegarm"})
	if err == nil || !strings.Contains(err.Error(), "unknown platform") {
		t.Fatalf("typo'd platform must still be rejected, got: %v", err)
	}
}
