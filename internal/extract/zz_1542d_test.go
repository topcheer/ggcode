package extract

import (
	"strings"
	"testing"
)

func Test1542D_UnparseableUDoesNotArmSubstituteDrops(t *testing.T) {

	res := rtfExtract(t, `{\rtf1\u99999999999 end}`)
	if !strings.Contains(res.Text, "end") {
		t.Fatalf("text after unparseable \\uN lost: %q", res.Text)
	}
}

func Test1542D_NarrowGateKnownTradeOff(t *testing.T) {
	res := rtfExtract(t, `{\rtf1\u233 ?x}`)

	if !strings.Contains(res.Text, "é") || !strings.Contains(res.Text, "x") {
		t.Fatalf("expected é+x, got %q", res.Text)
	}
	if strings.Contains(res.Text, "?") {
		t.Fatalf("substitute '?' should be dropped, got %q", res.Text)
	}
}
