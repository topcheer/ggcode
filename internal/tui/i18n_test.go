package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// #1725: machine-translation leftovers put Chinese strings in the German
// catalog ("厂商未找到" shown to de users). Guard the whole family: every
// non-zh catalog file must carry no CJK runes inside its return literals.
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
			// zh-CN/zh-TW/Japanese/Korean are CJK languages by design.
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
