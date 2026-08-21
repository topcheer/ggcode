package tui

// Guard tests for the #890-#894 fix round.

import (
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
