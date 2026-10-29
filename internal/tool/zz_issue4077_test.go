package tool

import (
	"strings"
	"testing"
)

// #4077: injectIntoMFlag used strings.IndexByte to find the closing quote of
// a quoted -m message - inside \" it hits the escaped quote, truncates the
// message mid-way, and leaves stray quotes/args outside -m (bash syntax
// error = no commit at all, or pathspec errors). The scan now tracks
// backslash escapes.
func TestIssue4077EscapedQuoteInjection(t *testing.T) {
	// findLastMFlag needs the -m position; use the exported shape through a
	// helper mirroring the active call path (run_command: findLastMFlag then
	// injectIntoMFlag).
	cmd := `git commit -m "fix \"typo\" in config"`
	mIdx := strings.LastIndex(cmd, "-m")
	out := injectIntoMFlag(cmd, mIdx)

	wantMsg := `fix \"typo\" in config` + "\n\n" + coAuthorTrailer
	if !strings.Contains(out, `"`+wantMsg+`"`) {
		t.Fatalf("full escaped message must survive injection:\n got: %s", out)
	}
	// The trailer must sit INSIDE the quoted message, and the tail after the
	// closing quote must be untouched.
	if !strings.HasSuffix(out, `"`) || strings.Count(out, `"`)%2 != 0 {
		t.Fatalf("quote balance broken: %s", out)
	}

	// Backslash-at-end (dangling escape) must not panic and must fall back
	// safely: no closing quote is found -> --trailer form.
	dangling := `git commit -m "tail \`
	out2 := injectIntoMFlag(dangling, strings.LastIndex(dangling, "-m"))
	if !strings.Contains(out2, "--trailer") {
		t.Fatalf("dangling escape must fall back to --trailer, got: %s", out2)
	}
}
