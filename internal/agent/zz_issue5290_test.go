package agent

import "testing"

// #5290: normalizeFilePath must fold case only on case-insensitive
// filesystems (darwin/windows), mirroring normalizeBatchPath. The former
// unconditional strings.ToLower merged case-distinct sibling files on Linux
// - inflating repetition warnings (false positives) and letting the
// premature-commit guard miss unread-file edits (false negatives).
func TestNormalizeFilePathPerOSFold(t *testing.T) {
	fold := batchPathFoldActive()

	// invariant on every OS: ./-prefix strip and trim are preserved
	if got := normalizeFilePath("  ./a/B.go "); got != normalizeFilePath("a/B.go") {
		t.Fatalf("trim/strip regression: %q vs %q", got, normalizeFilePath("a/B.go"))
	}

	a, b := normalizeFilePath("Foo.go"), normalizeFilePath("foo.go")
	if fold {
		if a != b {
			t.Fatalf("on case-insensitive OS, Foo.go and foo.go must fold equal: %q vs %q", a, b)
		}
	} else {
		if a == b {
			t.Fatalf("on case-sensitive OS, Foo.go and foo.go must stay distinct: both %q", a)
		}
	}
}

// Repetition keys must follow the same per-OS semantics (#5290).
func TestRepetitionFileKeyCaseSemantics(t *testing.T) {
	ka, kb := repetitionFileKey("Foo.go..."), repetitionFileKey("foo.go")
	if batchPathFoldActive() {
		if ka != kb {
			t.Fatalf("fold OS: repetition keys must match: %q vs %q", ka, kb)
		}
	} else if ka == kb {
		t.Fatalf("case-sensitive OS: repetition keys must differ: both %q", ka)
	}
}

// Premature-commit guard: reading one sibling must NOT mark the other as
// read on case-sensitive OSes (the #5290 false-negative path).
func TestPrematureCommitSiblingCaseNotMerged(t *testing.T) {
	s := newPrematureCommitState()
	s.filesRead[normalizeFilePath("Foo.go")] = true
	if batchPathFoldActive() {
		if !s.filesRead[normalizeFilePath("foo.go")] {
			t.Fatalf("fold OS: sibling read should be visible via folded key")
		}
	} else if s.filesRead[normalizeFilePath("foo.go")] {
		t.Fatalf("case-sensitive OS: sibling read must NOT leak into foo.go's key")
	}
}

// ASCII-only fold on fold-active OSes: non-ASCII bytes must not be
// Unicode-folded (divergence between the two families' old behavior).
func TestNormalizeFilePathASCIIFoldOnly(t *testing.T) {
	if !batchPathFoldActive() {
		t.Skip("case-sensitive host: ASCII fold not active")
	}
	if got, want := normalizeFilePath("Ä.go"), asciiFoldLower("Ä.go"); got != want {
		t.Fatalf("non-ASCII path normalized to %q, want batch_conflict-consistent %q", got, want)
	}
}
