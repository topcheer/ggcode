package extract

import (
	"strings"
	"testing"
)

// #1542-D: substitutes arm only on a SUCCESSFUL \uN. An out-of-range value
// must not arm ucSkip drops - the old code armed them while emitting nothing,
// silently swallowing the next N real text bytes.
func Test1542D_UnparseableUDoesNotArmSubstituteDrops(t *testing.T) {
	// \u99999999999 overflows int32: nothing emitted, and the 'end' after it
	// must survive in full (old code ate its first byte per ucSkip unit).
	res := rtfExtract(t, `{\rtf1\u99999999999 end}`)
	if !strings.Contains(res.Text, "end") {
		t.Fatalf("text after unparseable \\uN lost: %q", res.Text)
	}
}

// The known trade-off side stays pinned: a real '?' following a successful
// \uN that emitted no substitute is still consumed by the narrow gate.
func Test1542D_NarrowGateKnownTradeOff(t *testing.T) {
	res := rtfExtract(t, `{\rtf1\u233 ?x}`)
	// \u233 = é, ucSkip=1: the '?' is (correctly, per RTF spec) the substitute
	// and must not duplicate; 'x' survives.
	if !strings.Contains(res.Text, "é") || !strings.Contains(res.Text, "x") {
		t.Fatalf("expected é+x, got %q", res.Text)
	}
	if strings.Contains(res.Text, "?") {
		t.Fatalf("substitute '?' should be dropped, got %q", res.Text)
	}
}
