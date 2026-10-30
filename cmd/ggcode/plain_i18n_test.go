package main

import (
	"sort"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tui"
)

// TestPlainCatalogKeyParity ensures every key translated in one catalog
// exists in the other - a missing entry would silently fall back to English
// (or worse, print the raw key) mid-session.
func TestPlainCatalogKeyParity(t *testing.T) {
	en := plainCatalogs[tui.LangEnglish]
	zh := plainCatalogs[tui.LangZhCN]
	if len(en) == 0 || len(zh) == 0 {
		t.Fatalf("catalogs must be non-empty (en=%d zh=%d)", len(en), len(zh))
	}
	enKeys := make([]string, 0, len(en))
	for k := range en {
		enKeys = append(enKeys, k)
	}
	sort.Strings(enKeys)
	for _, k := range enKeys {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q missing from zh-CN catalog", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q missing from en catalog", k)
		}
	}
}

// TestPlainTrFallback verifies lookup order: active language, then English,
// then the raw key (never blank output).
func TestPlainTrFallback(t *testing.T) {
	if got := plainTr(tui.LangEnglish, "banner"); got == "" || got == "banner" {
		t.Errorf("en lookup failed: %q", got)
	}
	if got := plainTr(tui.LangZhCN, "banner"); got == "" || got == "banner" {
		t.Errorf("zh lookup failed: %q", got)
	}
	// Unsupported language falls back to English.
	if got := plainTr(tui.Language("fr"), "banner"); got != plainTr(tui.LangEnglish, "banner") {
		t.Errorf("unsupported language must fall back to en, got %q", got)
	}
	// Unknown key falls back to the key itself.
	if got := plainTr(tui.LangEnglish, "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key must echo the key, got %q", got)
	}
}

// TestPlainLangFromConfig verifies the startup derivation: empty stays
// English, configured values normalize (POSIX locale suffixes included).
func TestPlainLangFromConfig(t *testing.T) {
	if got := plainLangFromConfig(""); got != tui.LangEnglish {
		t.Errorf("empty config language = %q, want en", got)
	}
	if got := plainLangFromConfig("zh_CN.UTF-8"); got != tui.LangZhCN {
		t.Errorf("POSIX locale zh_CN.UTF-8 = %q, want zh-CN", got)
	}
	if got := plainLangFromConfig("en"); got != tui.LangEnglish {
		t.Errorf("en = %q, want en", got)
	}
}

// TestCfgLangNilSafe verifies the nil-config accessor used at startup.
func TestCfgLangNilSafe(t *testing.T) {
	if got := cfgLang(nil); got != "" {
		t.Errorf("cfgLang(nil) = %q, want empty", got)
	}
	if got := cfgLang(&config.Config{Language: "zh-CN"}); got != "zh-CN" {
		t.Errorf("cfgLang = %q, want zh-CN", got)
	}
}
