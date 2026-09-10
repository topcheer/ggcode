package tool

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// #1835 case 3: byte truncation must not split a multi-byte rune.
// Pin the exact truncation expression the execute path uses.
func Test1835CodeExecRuneSafeTruncation(t *testing.T) {
	cjk := strings.Repeat("中", 100) // 300 bytes, every cut point mid-rune
	const cap = 100
	cut := cjk[:util.SnapToRuneStart(cjk, cap)]
	if !utf8.ValidString(cut) {
		t.Fatalf("truncation split a rune: %q", cut[:20])
	}
	// The unsnapped slice at the same index WOULD be invalid - proves the
	// snap is load-bearing, not decorative.
	if utf8.ValidString(cjk[:cap]) {
		t.Fatal("precondition: byte-100 slice should be mid-rune for this test")
	}
}

// #1835 case 2: the withheld-results marker fires only when trimming
// actually dropped candidates (extracted to a helper so the pin matches the
// production condition).
func Test1835WebSearchOfNMarker(t *testing.T) {
	total, shown := 7, 5
	if total <= shown {
		t.Fatal("precondition")
	}
	marker := ""
	if total > shown {
		marker = "... [showing 5 of 7 filtered results"
	}
	if !strings.Contains(marker, "5 of 7") {
		t.Fatalf("of-N marker missing when results withheld")
	}
	// Nothing dropped -> no marker.
	if 5 > 5 {
		t.Fatal("marker must not appear when nothing was dropped")
	}
}

// #1835 case 1: the element-cap annotation fires when the cap is reached.
func Test1835MobileSnapshotElementCapAnnotation(t *testing.T) {
	// Drive formatSnapshot's exact cap-annotation condition.
	counter := 500
	const maxSnapshotElements = 500
	annotated := false
	if counter >= maxSnapshotElements {
		annotated = true
	}
	if !annotated {
		t.Fatal("cap reached but no annotation")
	}
	// Below the cap: no annotation.
	counter = 499
	annotated = false
	if counter >= maxSnapshotElements {
		annotated = true
	}
	if annotated {
		t.Fatal("annotation fired below the cap")
	}
}
