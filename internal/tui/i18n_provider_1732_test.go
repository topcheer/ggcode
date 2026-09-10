package tui

import (
	"os"
	"regexp"
	"testing"
)

// #1732: the provider module's shadowed keys are a stale-value trap -
// editing a key that the en/zh main catalogs switch on FIRST does
// nothing. Pin that no module key is shadowed by a main-catalog case,
// so any re-introduction of duplicates fails here.
func TestProviderModuleNotShadowedByMainCatalogs1732(t *testing.T) {
	modKeys := make(map[string]bool)
	for _, m := range []map[string]string{enProviderModule(), zhProviderModule()} {
		for k := range m {
			modKeys[k] = true
		}
	}
	caseRe := regexp.MustCompile(`case "([^"]+)":`)
	for _, f := range []string{"i18n_en.go", "i18n_zh.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Skipf("cannot read %s: %v", f, err)
		}
		for _, m := range caseRe.FindAllStringSubmatch(string(src), -1) {
			if modKeys[m[1]] {
				t.Fatalf("module key %q is shadowed by a case in %s - module edits silently do nothing", m[1], f)
			}
		}
	}
}
