package config

import (
	"os"
	"path/filepath"
	"testing"
)

func issue736WriteConfig(t *testing.T, dir, platform string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	yamlText := `
vendor: testv
endpoint: main
model: m1
vendors:
  testv:
    api_key: key736
    endpoints:
      main:
        protocol: openai
        base_url: https://test736.example.com/v1
im:
  adapters:
    tg:
      enabled: true
      platform: ` + platform + `
      bot_token: "123456:ABC-DEF"
`
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func TestIssue736_LoadNormalizesLegacyPlatformCasing(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue736WriteConfig(t, cfgDir, "Telegram")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := cfg.IM.Adapters["tg"]
	if !ok {
		t.Fatal("adapter tg not loaded")
	}
	if got.Platform != "telegram" {
		t.Fatalf("#736: Platform after Load = %q, want canonical %q", got.Platform, "telegram")
	}
}

func TestIssue736_SavePersistsCanonicalPlatform(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue736WriteConfig(t, cfgDir, "Discord")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg2, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	got, ok := cfg2.IM.Adapters["tg"]
	if !ok {
		t.Fatal("adapter tg not loaded after Save")
	}
	if got.Platform != "discord" {
		t.Fatalf("#736: Platform after Save+re-Load = %q, want canonical %q (bad data must self-heal)", got.Platform, "discord")
	}
}

func TestIssue736_UnknownPlatformLeftUntouched(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath := issue736WriteConfig(t, cfgDir, "telegarm")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := cfg.IM.Adapters["tg"]
	if !ok {
		t.Fatal("adapter tg not loaded")
	}
	if got.Platform != "telegarm" {
		t.Fatalf("unknown platform rewritten to %q, want untouched %q", got.Platform, "telegarm")
	}
}

func TestIssue736_NormalizeIMAdapterPlatforms(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IM.Adapters = map[string]IMAdapterConfig{
		"a": {Enabled: true, Platform: "  Telegram  "},
		"b": {Enabled: true, Platform: "WeCom"},
		"c": {Enabled: true, Platform: "slack"},
		"d": {Enabled: true, Platform: ""},
	}
	cfg.normalizeIMAdapterPlatforms()
	if cfg.IM.Adapters["a"].Platform != "telegram" {
		t.Fatalf("a: %q, want telegram", cfg.IM.Adapters["a"].Platform)
	}
	if cfg.IM.Adapters["b"].Platform != "wecom" {
		t.Fatalf("b: %q, want wecom", cfg.IM.Adapters["b"].Platform)
	}
	if cfg.IM.Adapters["c"].Platform != "slack" {
		t.Fatalf("c: %q, want slack (already canonical, unchanged)", cfg.IM.Adapters["c"].Platform)
	}
	if cfg.IM.Adapters["d"].Platform != "" {
		t.Fatalf("d: %q, want empty (skipped)", cfg.IM.Adapters["d"].Platform)
	}
}
