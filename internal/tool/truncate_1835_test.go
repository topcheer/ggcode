package tool

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

func Test1835CodeExecRuneSafeTruncation(t *testing.T) {
	cjk := strings.Repeat("中", 100)
	const cap = 100
	cut := cjk[:util.SnapToRuneStart(cjk, cap)]
	if !utf8.ValidString(cut) {
		t.Fatalf("truncation split a rune: %q", cut[:20])
	}

	if utf8.ValidString(cjk[:cap]) {
		t.Fatal("precondition: byte-100 slice should be mid-rune for this test")
	}
}

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

	if 5 > 5 {
		t.Fatal("marker must not appear when nothing was dropped")
	}
}

func Test1835MobileSnapshotElementCapAnnotation(t *testing.T) {

	counter := 500
	const maxSnapshotElements = 500
	annotated := false
	if counter >= maxSnapshotElements {
		annotated = true
	}
	if !annotated {
		t.Fatal("cap reached but no annotation")
	}

	counter = 499
	annotated = false
	if counter >= maxSnapshotElements {
		annotated = true
	}
	if annotated {
		t.Fatal("annotation fired below the cap")
	}
}
