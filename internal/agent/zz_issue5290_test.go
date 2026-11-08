package agent

import "testing"

// #5290: normalizeFilePath applied strings.ToLower unconditionally,
// collapsing case-distinct paths on case-sensitive filesystems (linux)
// so repetition_tracker and premature_commit merged distinct files
// (Foo.go vs foo.go) into one key. batch_conflict.go already had the
// correct behavior: fold only on case-insensitive platforms via
// ASCII-only folding.

func TestIssue5290_NoFoldOnLinuxSemantics(t *testing.T) {
	// Fold parity: normalizeFilePath must fold identically to
	// normalizeBatchPath (same platform gate + ASCII-only fold). The ./
	// strip is an intentional extra step here, so compare after it.
	inputs := []string{"Foo.go", "foo.go", "pkg/File.go", "ÜnïcodeFile.go", "aBc/DeF.go"}
	for _, in := range inputs {
		got := normalizeFilePath(in)
		want := normalizeBatchPath(in)
		if got != want {
			t.Errorf("input %q: normalizeFilePath=%q != normalizeBatchPath=%q", in, got, want)
		}
	}
	// Semantics pin: on case-insensitive platforms case-distinct inputs
	// merge; on case-sensitive ones they must stay distinct (the #5290 fix).
	a, b := normalizeFilePath("Foo.go"), normalizeFilePath("foo.go")
	if batchPathFoldActive() {
		if a != b {
			t.Errorf("fold-active platform should merge case-distinct paths: %q vs %q", a, b)
		}
	} else if a == b {
		t.Errorf("case-sensitive platform must keep Foo.go and foo.go distinct (both %q)", a)
	}
}

func TestIssue5290_TrimsAndDedotsUnconditionally(t *testing.T) {
	// trimming and ./ stripping are platform-independent and must keep
	// working regardless of fold decision
	if got := normalizeFilePath("  ./pkg/File.go  "); got != "pkg/File.go" && got != asciiFoldLower("pkg/File.go") {
		t.Errorf("trim/dot strip broken: %q", got)
	}
}

func TestIssue5290_RepetitionKeyStable(t *testing.T) {
	// repetitionFileKey must produce the same key for a path and its
	// trimmed form (regression pin for the tracking path)
	a := repetitionFileKey("pkg/File.go")
	b := repetitionFileKey("  ./pkg/File.go  ")
	if a != b {
		t.Errorf("repetitionFileKey not stable under trim/dot: %q vs %q", a, b)
	}
}
