package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// Issue #627 defect 1: the suffix scan in lookupReadHash/recordWriteHash
// must not resolve a relative path to an arbitrary same-basename sibling.
// With two different "main.go" files recorded under absolute keys, a
// relative lookup for "main.go" is ambiguous and must be skipped, never
// answered with a random sibling's hash.
func TestIssue627_AmbiguousSuffixMatchSkipped(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "svc-a", "main.go")
	b := filepath.Join(root, "svc-b", "main.go")
	if err := os.MkdirAll(filepath.Dir(a), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(a)
	tr.recordReadHash(b)

	// The ambiguous relative form must not resolve to a sibling's hash
	// (either content hash would be a wrong answer for the other file).
	if h, ok := lookupReadHash(tr, "main.go"); ok {
		t.Fatalf("ambiguous suffix lookup resolved to sibling hash %d (#627)", h)
	}

	// Unambiguous relative forms still resolve (#557 must keep working).
	// Note: a bare "main.go" is inherently ambiguous with two same-basename
	// keys — the disambiguating relative form carries the parent dir.
	if h, ok := lookupReadHash(tr, "svc-a/main.go"); !ok {
		t.Fatal("unique suffix lookup regressed (#557)")
	} else if want := hashFilePrefix(a); h != want {
		t.Fatalf("unique suffix lookup returned wrong hash: got %d want %d", h, want)
	}
}

// recordWriteHash with a relative form must not evict a sibling's hash when
// multiple same-basename candidates exist (#627 write side).
func TestIssue627_WriteEvictDoesNotDeleteSibling(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "x", "config.go")
	b := filepath.Join(root, "y", "config.go")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package cfg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tr := newReadHashTracker()
	tr.recordReadHash(a)
	tr.recordReadHash(b)

	// Ambiguous relative write: neither sibling hash may be evicted.
	tr.recordWriteHash("config.go")
	if _, ok := lookupReadHash(tr, a); !ok {
		t.Error("recordWriteHash evicted sibling hash for ambiguous relative form (#627)")
	}
	if _, ok := lookupReadHash(tr, b); !ok {
		t.Error("recordWriteHash evicted sibling hash for ambiguous relative form (#627)")
	}

	// Unambiguous relative write still clears the resolved entry (#557).
	tr.recordWriteHash("x/config.go")
	if _, ok := lookupReadHash(tr, a); ok {
		t.Error("recordWriteHash failed to clear the resolved sibling's hash")
	}
	if _, ok := lookupReadHash(tr, b); !ok {
		t.Error("recordWriteHash must not clear the OTHER same-basename file's hash")
	}
}

// Issue #627 defect 2: a tail edit beyond the old 16KB prefix (but within
// the 1MB window) must be detected even when mtime is restored — the exact
// blind spot the issue probe demonstrated.
func TestIssue627_TailSubSecondEditDetected(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tail.go")

	content := make([]byte, 32*1024)
	for i := range content {
		content[i] = byte(i % 241)
	}
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	orig := fi.ModTime()

	tr := newReadHashTracker()
	tr.recordReadHash(f)

	// Edit at byte 31KB (deep in the old 16KB-prefix blind spot) and restore
	// the mtime so the mtime path cannot catch it.
	content[31*1024] ^= 0xFF
	if err := os.WriteFile(f, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(f, orig, orig); err != nil {
		t.Fatal(err)
	}

	if got := tr.validateContentAtEdit(f, 100); got == "" {
		t.Error("tail edit beyond old 16KB prefix + restored mtime went undetected (#627)")
	}
}
