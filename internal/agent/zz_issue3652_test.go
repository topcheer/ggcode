package agent

// #3652 probe: the sed exemption must recognize every delimiter sed
// accepts (s|a|b|, s#a#b#, s,a,b,) and the unquoted form, in both quote
// styles - a legitimate skip-marker REMOVAL typed with any of them used to
// fail the exemption and fire the exact opposite warning ("Command inserts
// skip markers").

import "testing"

func TestIssue3652_AnyDelimiterRemovalExempt(t *testing.T) {
	legit := []string{
		"sed -i 's|t.Skip(|t.Log(|' foo_test.go",
		"sed -i 's#t.Skip(#t.Log(#' foo_test.go",
		"sed -i \"s,t.Skip(,t.Log(,\" foo_test.go",
		"sed -i s/t.Skip(/t.Log(/g foo_test.go",    // unquoted
		"sed -i \"s/t.Skip(/t.Log(/\" foo_test.go", // #3628 regression guard
		"sed -i 's/t.Skip(//g' foo_test.go",        // original form
	}
	for _, cmd := range legit {
		if !isSedSkipRemoval(cmd) {
			t.Fatalf("legitimate skip removal not exempted: %q", cmd)
		}
	}
	// Injections stay flagged in every delimiter style.
	inject := []string{
		"sed -i 's|assert|t.Skip(|' foo_test.go",
		"sed -i \"s,assert,t.Skip(,\" foo_test.go",
		"sed -i s/assert/t.Skip(/g foo_test.go",
	}
	for _, cmd := range inject {
		if isSedSkipRemoval(cmd) {
			t.Fatalf("skip injection wrongly exempted: %q", cmd)
		}
	}
}

func TestIssue3652_UrlLikeStringsNotSedExpressions(t *testing.T) {
	// A URL containing "s/" must not be treated as a sed expression - the
	// fallback splits only on quote- or space-prefixed s/.
	if isSedSkipRemoval("curl https://example.com/t.Skip(/raw") {
		t.Fatal("URL misparsed as a sed skip-removal expression")
	}
}
