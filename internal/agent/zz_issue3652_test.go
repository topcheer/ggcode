package agent

import "testing"

func TestIssue3652_AnyDelimiterRemovalExempt(t *testing.T) {
	legit := []string{
		"sed -i 's|t.Skip(|t.Log(|' foo_test.go",
		"sed -i 's#t.Skip(#t.Log(#' foo_test.go",
		"sed -i \"s,t.Skip(,t.Log(,\" foo_test.go",
		"sed -i s/t.Skip(/t.Log(/g foo_test.go",
		"sed -i \"s/t.Skip(/t.Log(/\" foo_test.go",
		"sed -i 's/t.Skip(//g' foo_test.go",
	}
	for _, cmd := range legit {
		if !isSedSkipRemoval(cmd) {
			t.Fatalf("legitimate skip removal not exempted: %q", cmd)
		}
	}

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

	if isSedSkipRemoval("curl https://example.com/t.Skip(/raw") {
		t.Fatal("URL misparsed as a sed skip-removal expression")
	}
}
