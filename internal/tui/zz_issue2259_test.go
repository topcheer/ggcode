package tui

// #2259: parseCatalogCases dropped any case block whose body carried a
// comment or blank line between case and return - the house convention
// for translation-decision notes - so exactly the sensitive keys (the
// #1372/#1373/#1416 verb-round fixes) silently left the VerbConsistency
// line. The real dormant mismatch this hid: ja lang.current (1 %s) vs
// en (2 %s).

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue2259CommentedCaseStillParsed(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "i18n_xx.go")
	src := `package tui

func xxCatalog(key string) string {
	switch key {
	case "k.one":
		// translation decision note (#1372 style)
		return "value one %s"
	case "k.two":

		return "value two"
	}
	return ""
}
`
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	entries := parseCatalogCases(t, f)
	if entries["k.one"] != "value one %s" {
		t.Fatalf("commented case must still be parsed, got %q", entries["k.one"])
	}
	if entries["k.two"] != "value two" {
		t.Fatalf("blank-line case must still be parsed, got %q", entries["k.two"])
	}
}

func TestIssue2259JALangCurrentVerbAligned(t *testing.T) {
	en := parseCatalogCases(t, "i18n_en.go")
	ja := parseCatalogCases(t, "i18n_ja.go")
	if _, ok := en["lang.current"]; !ok {
		t.Fatal("en lang.current must be visible to the consistency line")
	}
	got, ok := ja["lang.current"]
	if !ok {
		t.Fatal("ja lang.current must be visible (was silently dropped by the comment blindspot)")
	}
	if verbCounts(en["lang.current"]) != verbCounts(got) {
		t.Fatalf("verb mismatch: en=%s ja=%s", verbCounts(en["lang.current"]), verbCounts(got))
	}
}
