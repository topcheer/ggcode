package agent

import (
	"strings"
	"testing"
)

// Issue #1192: the delta fingerprint (fnName|varName) has no rename
// tolerance. A pure function rename re-reported an existing leak and burned
// the single maxIntegrityWarnings slot. Mirrors the nil_deref_check suffixKey
// fallback (#1179) with the #1186 lesson applied: token/multiset based, each
// old instance suppresses at most one new instance.

func TestResourceLeakRenameNotReported(t *testing.T) {
	old := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
`
	// Pure rename: same body, different function name.
	renamed := `package p
import "os"
func loadBar() {
	f, _ := os.Open("x")
	_ = f
}
`
	if w := checkResourceLeaks("test.go", old, renamed); len(w) != 0 {
		t.Errorf("pure function rename must not re-report existing leak, got: %v", w)
	}
}

func TestResourceLeakRenamePlusUnrelatedEditNotReported(t *testing.T) {
	old := `package p
import "net/http"
func fetchFoo(url string) {
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	_ = resp
}
`
	renamed := `package p
import "net/http"

// doc comment added in the same edit
func fetchBar(url string) {
	resp, err := http.Get(url)
	if err != nil {
		return
	}
	_ = resp
}
`
	if w := checkResourceLeaks("test.go", old, renamed); len(w) != 0 {
		t.Errorf("rename + cosmetic edit must not re-report existing leak, got: %v", w)
	}
}

func TestResourceLeakNewSameShapeInstanceStillReported(t *testing.T) {
	old := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
`
	// Old function still exists; a new same-shape instance in another
	// function is genuinely new and must be reported.
	edited := old + `
func loadOther() {
	f, _ := os.Open("x")
	_ = f
}
`
	w := checkResourceLeaks("test.go", old, edited)
	if len(w) != 1 {
		t.Fatalf("new same-shape leak in another function must be reported exactly once, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "Possible resource leak") || !strings.Contains(w[0], "variable f") {
		t.Errorf("warning should describe the new leak, got: %s", w[0])
	}
}

func TestResourceLeakRenameMultisetNoCrossAbsorption(t *testing.T) {
	// #1186 lesson: rename fallback consumes one token per old instance, so a
	// rename cannot also absorb a second, genuinely new same-shape instance.
	old := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
`
	renamedPlusNew := `package p
import "os"
func loadBar() {
	f, _ := os.Open("x")
	_ = f
}

func loadExtra() {
	f, _ := os.Open("x")
	_ = f
}
`
	w := checkResourceLeaks("test.go", old, renamedPlusNew)
	if len(w) != 1 {
		t.Fatalf("rename token must suppress exactly one instance; the added leak must still be reported, got %d: %v", len(w), w)
	}
	if !strings.Contains(w[0], "Possible resource leak") || !strings.Contains(w[0], "variable f") {
		t.Errorf("warning should describe the newly added leak, got: %s", w[0])
	}
}

func TestResourceLeakTwoRenamedTwoTokens(t *testing.T) {
	// Two old leaks, both functions renamed: both suppressed, nothing new.
	old := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
func loadBaz() {
	g, _ := os.Open("y")
	_ = g
}
`
	renamed := `package p
import "os"
func loadBar() {
	f, _ := os.Open("x")
	_ = f
}
func loadQux() {
	g, _ := os.Open("y")
	_ = g
}
`
	if w := checkResourceLeaks("test.go", old, renamed); len(w) != 0 {
		t.Errorf("both renamed leaks must be suppressed, got: %v", w)
	}
}

func TestResourceLeakRenameFallbackRequiresOldNameGone(t *testing.T) {
	// If the old function name still exists in the new file, the fallback
	// must not fire: the same-shape instance elsewhere is new.
	old := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
`
	edited := `package p
import "os"
func loadFoo() {
	f, _ := os.Open("x")
	_ = f
}
func loadBar() {
	f, _ := os.Open("x")
	_ = f
}
`
	w := checkResourceLeaks("test.go", old, edited)
	if len(w) != 1 {
		t.Fatalf("expected 1 warning for new same-shape leak while old name persists, got %d: %v", len(w), w)
	}
}
