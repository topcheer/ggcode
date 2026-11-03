package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestNonZhCatalogsContainNoCJK1725(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Skip("cannot read package dir:", err)
	}
	cjk := regexp.MustCompile(`[\x{4e00}-\x{9fff}\x{3000}-\x{303f}\x{ff00}-\x{ffef}]`)
	ret := regexp.MustCompile(`return\s+"[^"]*"`)
	bad := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "i18n_") || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.Contains(name, "zh") || strings.Contains(name, "_ja") || strings.Contains(name, "_ko") {

			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, m := range ret.FindAllStringSubmatch(string(data), -1) {
			if cjk.MatchString(m[0]) {
				t.Errorf("%s: CJK in return literal: %s", name, m[0])
				bad++
			}
		}
	}
	if bad == 0 && testing.Short() {
		t.Log("no CJK literals found in non-zh catalogs")
	}
}

func TestLocalizeSlashDescriptionSessionPinTags(t *testing.T) {
	cmds := []string{"/pin-session", "/unpin-session", "/tag", "/untag", "/tags"}
	langs := []Language{"en", "zh", "de"}
	for _, lang := range langs {
		for _, cmd := range cmds {
			if got := localizeSlashDescription(lang, cmd); got == cmd {
				t.Errorf("%s: %s has no translation (fell back to raw command)", lang, cmd)
			}
		}
	}
}
