//go:build windows

package update

// #1832 case 2 regression: quoteArgs' quoted branch appended each rune with
// byte(c), truncating code points > 255 (CJK usernames) to their low byte
// and destroying the UTF-8 sequence before StringToUTF16Ptr conversion - a
// manifest path like C:\Users\张三 伟\.ggcode\... reached the elevated
// helper as mojibake and the update could not find its manifest.

import (
	"strings"
	"testing"
)

func TestQuoteArgsPreservesNonASCIIInQuotedArgs(t *testing.T) {
	manifest := `C:\Users\张三 伟\.ggcode\update-manifest.json`
	got := quoteArgs([]string{"runas", manifest})
	// The CJK characters must survive byte-for-byte inside the quotes.
	if !strings.Contains(got, `张三 伟`) {
		t.Fatalf("CJK path mangled in quoted arg: %q", got)
	}
	if !strings.HasPrefix(got, `runas "`) || !strings.HasSuffix(got, `"`) {
		t.Fatalf("quoting shape wrong: %q", got)
	}
}

func TestQuoteArgsBackslashAndQuoteEscapingStillEscapes(t *testing.T) {
	got := quoteArgs([]string{`a b\"c`})
	if !strings.Contains(got, `\\\"`) {
		t.Fatalf("backslash-before-quote escaping lost: %q", got)
	}
}

func TestQuoteArgsUnquotedNonASCIIUnchanged(t *testing.T) {
	// No space/tab/quote: the arg must pass through as-is (bytes verbatim).
	arg := `C:\Users\张三\.ggcode\manifest.json`
	if got := quoteArgs([]string{arg}); got != arg {
		t.Fatalf("unquoted non-ASCII arg changed: %q", got)
	}
}
