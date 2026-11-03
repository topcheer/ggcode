package tool

import (
	"strings"
	"testing"
)

func TestIssue4077EscapedQuoteInjection(t *testing.T) {

	cmd := `git commit -m "fix \"typo\" in config"`
	mIdx := strings.LastIndex(cmd, "-m")
	out := injectIntoMFlag(cmd, mIdx)

	wantMsg := `fix \"typo\" in config` + "\n\n" + coAuthorTrailer
	if !strings.Contains(out, `"`+wantMsg+`"`) {
		t.Fatalf("full escaped message must survive injection:\n got: %s", out)
	}

	if !strings.HasSuffix(out, `"`) || strings.Count(out, `"`)%2 != 0 {
		t.Fatalf("quote balance broken: %s", out)
	}

	dangling := `git commit -m "tail \`
	out2 := injectIntoMFlag(dangling, strings.LastIndex(dangling, "-m"))
	if !strings.Contains(out2, "--trailer") {
		t.Fatalf("dangling escape must fall back to --trailer, got: %s", out2)
	}
}
