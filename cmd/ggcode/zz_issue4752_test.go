package main

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tui"
)

// #4752 defect 2: switchHint must echo the ENDPOINT name (the just-added
// endpoint is not active), never the default model id.
func TestIssue4752_SwitchHintUsesEndpointName(t *testing.T) {
	prev := plainLang
	plainLang = tui.LangEnglish
	t.Cleanup(func() { plainLang = prev })
	h := switchHint("myep")
	if !strings.Contains(h, "/endpoint myep") {
		t.Fatalf("hint must reference the endpoint name, got %q", h)
	}
	if strings.Contains(h, "gpt") {
		t.Fatalf("hint must not leak model ids, got %q", h)
	}
	// A model id passed as the endpoint name would previously produce a hint
	// targeting a nonexistent endpoint; with the fixed semantics the hint is
	// built from add.switchhint + name in the active language.
	if h != plainTr(tui.LangEnglish, "add.switchhint")+"myep" {
		t.Fatalf("hint must be catalog-based, got %q", h)
	}
	if switchHint("") != "" {
		t.Fatal("empty name must yield no hint")
	}
}

// #4752 defect 1: the previously dead catalog keys must exist in BOTH
// catalogs (the wiring added three savefail keys; the rest were verified
// present and are now actually called).
func TestIssue4752_CatalogCoversWiredKeys(t *testing.T) {
	keys := []string{
		"switch.ok", "flags.bad", "add.req", "add.proto", "add.novendor",
		"add.duplicate", "add.ok", "add.savefail", "add.switchhint",
		"list.more", "models.discovered", "models.none", "models.nodiscovery",
		"resolve.failed", "setkey.req", "setkey.noactive", "setkey.nofound",
		"setkey.ok", "setkey.savefail", "addmodel.req", "addmodel.noactive",
		"addmodel.nofound", "addmodel.duplicate", "addmodel.ok",
		"addmodel.savefail",
	}
	for _, lang := range []tui.Language{tui.LangEnglish, tui.LangZhCN} {
		cat, ok := plainCatalogs[lang]
		if !ok {
			t.Fatalf("catalog missing for %v", lang)
		}
		for _, k := range keys {
			if v, ok := cat[k]; !ok || strings.TrimSpace(v) == "" {
				t.Fatalf("%v catalog missing/empty key %q", lang, k)
			}
		}
	}
}

// The wired format keys must keep their verbs so Printf sites match: add.ok
// takes two %q plus the hint suffix; addmodel.ok takes %q %q %s.
func TestIssue4752_FormatVerbsPreserved(t *testing.T) {
	for _, lang := range []tui.Language{tui.LangEnglish, tui.LangZhCN} {
		if got := strings.Count(plainTr(lang, "add.ok"), "%q"); got != 2 {
			t.Fatalf("%v add.ok must have two %%q verbs, got %d", lang, got)
		}
		if got := strings.Count(plainTr(lang, "addmodel.ok"), "%q"); got != 2 {
			t.Fatalf("%v addmodel.ok must have two %%q verbs, got %d", lang, got)
		}
		if !strings.Contains(plainTr(lang, "addmodel.ok"), "%s") {
			t.Fatalf("%v addmodel.ok must keep its %%s verb", lang)
		}
	}
}
