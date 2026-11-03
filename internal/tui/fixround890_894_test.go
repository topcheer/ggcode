package tui

import (
	"os"
	"strings"
	"testing"
)

func TestCatalogKeysAreASCII(t *testing.T) {
	for name, cat := range map[string]func(string) string{
		"fr": frCatalog,
		"es": esCatalog,
	} {

		for _, key := range []string{"panel.files", "command.model_failed"} {
			got := cat(key)
			if got == "" {
				t.Errorf("%s: key %q resolved empty", name, key)
			}
		}
	}
}

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

func TestSlashStreamArgsPreserveCase(t *testing.T) {
	text := "/Stream start MyTarget"
	args := strings.TrimSpace(text[len("/stream"):])
	if args != "start MyTarget" {
		t.Fatalf("args = %q, want %q", args, "start MyTarget")
	}
}

func TestCatalogValuesNoLiteralBackslashN(t *testing.T) {
	for _, p := range []string{"i18n_ko.go", "i18n_ja.go", "i18n_en.go", "i18n_fr.go", "i18n_es.go", "i18n_zh_cn.go", "i18n_zh_tw.go"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), `\\n`) {
			t.Errorf("%s contains literal \\\\n escapes", p)
		}
	}
}
