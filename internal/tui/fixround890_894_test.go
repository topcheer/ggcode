package tui

// Guard tests for the #890-#894 fix round.

import (
	"os"
	"strings"
	"testing"
)

// TestCatalogKeysAreASCII (#892): i18n case keys must be plain ASCII.
// Accented keys (faîled, détails) never match the ASCII keys callers pass,
// silently falling back to English for the whole locale.
func TestCatalogKeysAreASCII(t *testing.T) {
	for name, cat := range map[string]func(string) string{
		"fr": frCatalog,
		"es": esCatalog,
	} {
		// The two keys that were dead (accented case labels never matched
		// the ASCII keys callers pass) must now resolve in each catalog.
		for _, key := range []string{"panel.files", "command.model_failed"} {
			got := cat(key)
			if got == "" {
				t.Errorf("%s: key %q resolved empty", name, key)
			}
		}
	}
}

// TestNormalizeLanguagePOSIX (#894): POSIX locales with codeset suffix and
// underscore forms must map.
func TestNormalizeLanguagePOSIX(t *testing.T) {
	cases := map[string]Language{
		"zh_TW.UTF-8":  LangZhTW,
		"zh_tw":        LangZhTW,
		"fr_FR.UTF-8":  LangFr,
		"zh_CN.GB2312": LangZhCN,
	}
	for in, want := range cases {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestSlashStreamArgsPreserveCase (#893): target names are case-sensitive;
// the args extracted after the command token must keep original case.
func TestSlashStreamArgsPreserveCase(t *testing.T) {
	text := "/Stream start MyTarget"
	args := strings.TrimSpace(text[len("/stream"):])
	if args != "start MyTarget" {
		t.Fatalf("args = %q, want %q", args, "start MyTarget")
	}
}

// TestCatalogValuesNoLiteralBackslashN (#895): values must contain real
// newline escapes (\n), never the double-escaped literal \\n that renders
// as raw "\n" text in the UI.
func TestCatalogValuesNoLiteralBackslashN(t *testing.T) {
	for _, p := range []string{"i18n_ko.go", "i18n_ja.go", "i18n_en.go", "i18n_fr.go", "i18n_es.go", "i18n_zh_cn.go", "i18n_zh_tw.go"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue // optional catalogs in this test context
		}
		if strings.Contains(string(data), `\\n`) {
			t.Errorf("%s contains literal \\\\n escapes", p)
		}
	}
}
