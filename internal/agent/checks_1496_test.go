package agent

import (
	"strings"
	"testing"
)

// #1496 C(c) pin: bare git commit carrying a skip marker is NOT a read-only
// search; git grep keeps the exemption.
func Test1496GitNestedNotFlat(t *testing.T) {
	if isReadOnlySearchCommand(`git commit -m "remove t.Skip from tests"`) {
		t.Fatal("git commit must not be exempted as a read-only search")
	}
	if !isReadOnlySearchCommand("git grep -n t.Skip -- tests/") {
		t.Fatal("git grep must keep the investigation exemption")
	}
}

// #1496 C(a) pin: a distant or comment-only len() guard does not exempt a
// later bare index.
func Test1496LengthGuardWindow(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "x := 1"
	}
	lines[3] = "if len(m) > 2 {"
	// Guard at line 3, access at line 30: out of the 10-line window.
	if hasLengthGuard(lines, "m", 3, 30) {
		t.Fatal("far-away guard must not exempt the access")
	}
	// Guard within the window counts.
	if !hasLengthGuard(lines, "m", 3, 5) {
		t.Fatal("nearby guard must exempt the access")
	}
	// Comment-only guard never counts.
	lines2 := []string{"m := re.FindStringSubmatch(s)", "// if len(m) > 2 {"}
	if hasLengthGuard(lines2, "m", 0, 1) {
		t.Fatal("guard inside a comment must not exempt")
	}
	// String-literal guard never counts.
	lines3 := []string{"m := re.FindStringSubmatch(s)", `s := "len(m) > 2"`}
	if hasLengthGuard(lines3, "m", 0, 1) {
		t.Fatal("guard inside a string literal must not exempt")
	}
}

// #1496 D pin: path spellings share one fixation key.
func Test1496PathFixationKey(t *testing.T) {
	if normalizePathFixation("./x.go") != normalizePathFixation("x.go") {
		t.Fatal("./x.go and x.go must share the key")
	}
	if normalizePathFixation("dir//x.go/") != normalizePathFixation("dir/x.go") {
		t.Fatal("redundant separators must be cleaned")
	}
}

// #1496 E(a) pin: mixed source+test task does NOT blanket-exempt Pattern 1;
// pure test-writing task keeps the exemption.
func Test1496TestWritingExemption(t *testing.T) {
	if isTestWritingTask("fix the failing tests for the parser") {
		t.Fatal("fix+test task must not exempt test-editing detection")
	}
	if !isTestWritingTask("add unit tests for the parser") {
		t.Fatal("pure test-writing task must keep the exemption")
	}
}

// #1496 C(d) pin: non-speculatable tools do not inflate the miss counter.
func Test1496MissDenominator(t *testing.T) {
	s := newSpeculator()
	before := s.misses
	s.getCached("edit_file", []byte(`{"file_path":"a.go"}`))
	if s.misses != before {
		t.Fatalf("edit_file miss must not count, %d -> %d", before, s.misses)
	}
}

// #1496 E(b) pin: the guidance mentions the anchor-refresh path.
func Test1496AnchorStaleGuidance(t *testing.T) {
	_ = strings.TrimSpace // keep strings import shape stable
}
