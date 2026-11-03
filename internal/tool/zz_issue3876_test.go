//go:build darwin && goolm

package tool

import (
	"strings"
	"testing"
)

func TestIssue3876_KeyComboEmptyRejected(t *testing.T) {
	for _, combo := range []string{"", "   ", "+"} {
		_, err := keyComboResult(t.Context(), combo)
		if err == nil {
			t.Fatalf("keyComboResult(%q) must fail with a clear error, got nil (#3876)", combo)
		}
		if !strings.Contains(err.Error(), "key combo") {
			t.Fatalf("error should name the key combo, got: %v", err)
		}
	}
}

func TestIssue3876_KeyComboTrailingPlusRejected(t *testing.T) {

	if _, err := keyComboResult(t.Context(), "cmd+"); err == nil {
		t.Fatal("trailing '+' must be rejected with a clear error (#3876)")
	}
}

func TestIssue3876_AppleScriptQuoteControlChars(t *testing.T) {
	got := applescriptQuote("a\nb\rc\td\\e\"f")
	want := "\"a\\nb\\rc\\td\\\\e\\\"f\""
	if got != want {
		t.Fatalf("applescriptQuote control chars: got %s, want %s (#3876)", got, want)
	}
}
