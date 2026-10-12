package agent

// #4015 companion tests: stripping the strategist completion marker must take
// slice offsets from the ORIGINAL summary. The old
// strings.Index(strings.ToUpper(summary), marker) offset belongs to the
// uppercase copy and drifts past every char whose ToUpper changes byte length
// (German ß→SS, ligature ﬁ→FI), corrupting the summary head.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4015_IndexFoldBasics(t *testing.T) {
	cases := []struct {
		name    string
		s       string
		marker  string
		wantOff int
		wantLen int
	}{
		{"exact ascii", "done <GOAL_ACHIEVED/> summary", strategistCompleteMarker, 5, len(strategistCompleteMarker)},
		{"lowercase form", "done <goal_achieved/> summary", strategistCompleteMarker, 5, len(strategistCompleteMarker)},
		{"no match", "no marker here", strategistCompleteMarker, -1, 0},
		{"empty marker", "anything", "", 0, 0},
		{"match at start", "<GOAL_ACHIEVED/>tail", strategistCompleteMarker, 0, len(strategistCompleteMarker)},
	}
	for _, tc := range cases {
		off, l := indexFold(tc.s, tc.marker)
		if off != tc.wantOff || l != tc.wantLen {
			t.Errorf("%s: indexFold = (%d,%d), want (%d,%d)", tc.name, off, l, tc.wantOff, tc.wantLen)
		}
	}
}

// Core regression: a length-changing uppercase char BEFORE the marker must not
// shift the slice offset. ß uppercases to SS (+1 byte in the uppercase copy),
// which is exactly the drift that corrupted summaries before #4015.
func TestIssue4015_OffsetStableAcrossUppercaseExpansion(t *testing.T) {
	// Drift fixture (empirically verified Go behavior): dotless i U+0131
	// (2 bytes) uppercases to I (1 byte) via the SIMPLE single-rune mapping -
	// a real byte-length change under strings.ToUpper. (The issue body's
	// examples do NOT drift in Go: special casings like ß→SS and ﬁ→FI are not
	// applied by strings.ToUpper - both stay byte-neutral.) The OLD formula
	// slices the original at uppercaseIdx+len(marker), landing one byte EARLY
	// inside the lowercase marker's tail. indexFold returns original offsets.
	s := "\u0131dentifier check <goal_achieved/> final summary"
	off, l := indexFold(s, strategistCompleteMarker)
	if off < 0 {
		t.Fatalf("marker not found")
	}
	after := strings.TrimSpace(s[off+l:])
	if after != "final summary" {
		t.Fatalf("after-marker slice = %q, want %q (uppercase-copy offset drift)", after, "final summary")
	}
	// Contrast: the old formula's offset lives in the uppercase copy and is
	// shifted by the ß→SS expansion, slicing the original at the wrong place.
	oldIdx := strings.Index(strings.ToUpper(s), strategistCompleteMarker)
	oldAfter := strings.TrimSpace(s[oldIdx+len(strategistCompleteMarker):])
	if oldAfter == after {
		t.Fatalf("expected old formula to differ on this fixture; oldAfter=%q after=%q", oldAfter, after)
	}
}

// Multi-byte marker must not match across rune boundaries.
func TestIssue4015_NoCrossRuneFalseMatch(t *testing.T) {
	// A rune whose UTF-8 bytes happen to contain an ASCII byte of the marker
	// (U+003C '<' cannot appear in a multi-byte rune's payload since payload
	// bytes are >= 0x80, so use a lower-byte overlap via 'A'=0x41 in a 2-byte
	// rune payload? Payloads are >= 0x80 too - so ASCII markers structurally
	// cannot false-match mid-rune; pin that invariant anyway with a CJK lead.)
	if off, _ := indexFold("中文<GOAL_ACHIEVED/>", strategistCompleteMarker); off != len("中文") {
		t.Fatalf("offset after CJK lead = %d, want %d", off, len("中文"))
	}
}

// Source guard: the drift-prone old pattern must stay gone from the strip site.
func TestIssue4015_StripSiteUsesIndexFold(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("agent.go not readable: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "strings.Index(strings.ToUpper(summary), strategistCompleteMarker)") {
		t.Fatal("old drift-prone strip pattern is back (uppercase-copy offset sliced into original summary)")
	}
	if !strings.Contains(text, "indexFold(summary, strategistCompleteMarker)") {
		t.Fatal("strip site no longer calls indexFold - #4015 wiring lost")
	}
}
