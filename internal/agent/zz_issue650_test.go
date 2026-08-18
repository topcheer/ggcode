package agent

// #650: recordWriteHash resolved the opposite-form key via the unique-suffix
// scan (#627) — when a same-basename sibling made that scan ambiguous, the
// SAME file's other-form key was left behind and the next edit in that form
// hit a stale hash against post-write content, mis-reporting "content
// changed" on the agent's own edit. Eviction must canonicalize: delete every
// key that is the same file, keep sibling keys.

import (
	"os"
	"path/filepath"
	"testing"
)

// Same file recorded in two legal forms (absolute + relative) alongside a
// same-basename sibling: a write in the absolute form must clear BOTH forms
// of that file and keep the sibling's hash (pre-fix: relative key survived,
// next relative-form edit mis-reported "content changed").
func TestIssue650_WriteClearsOtherFormKeyDespiteSiblingAmbiguity(t *testing.T) {
	root := t.TempDir()
	svcA := filepath.Join(root, "svc-a")
	svcB := filepath.Join(root, "svc-b")
	if err := os.MkdirAll(svcA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(svcB, 0o755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(svcA, "main.go")
	b := filepath.Join(svcB, "main.go")
	if err := os.WriteFile(a, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("package b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rel := filepath.Join("svc-a", "main.go") // relative form of file a
	tr := newReadHashTracker()
	tr.recordReadHash(a)   // absolute form
	tr.recordReadHash(rel) // relative form of the SAME file
	tr.recordReadHash(b)   // sibling (same basename, different file)

	// Write to file a via the ABSOLUTE form. The relative form is suffix-
	// ambiguous with the sibling's absolute key, so the old unique-suffix
	// resolver gave up — leaving the relative-form key stale.
	tr.recordWriteHash(a)

	// The same file's other form must be gone (pre-fix: survived → the next
	// relative-form edit compared post-write content against the stale hash
	// and mis-reported "content changed").
	if _, ok := lookupReadHash(tr, rel); ok {
		t.Fatal("recordWriteHash left the same file's other-form key behind under sibling ambiguity (#650)")
	}
	if _, ok := lookupReadHash(tr, a); ok {
		t.Fatal("recordWriteHash left the exact key behind")
	}
	// The sibling must survive (#627 regression guard).
	if _, ok := lookupReadHash(tr, b); !ok {
		t.Fatal("recordWriteHash evicted the same-basename sibling's hash (#627 regression)")
	}
}

// End-to-end: after the agent edits a file, a subsequent edit in the other
// path form must NOT warn "content changed" on its own write.
func TestIssue650_NoSelfEditFalsePositiveAfterWrite(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)  // absolute form at read time
	tr.recordWriteHash(f) // agent writes via absolute form

	// A later edit arrives in the RELATIVE form; content is exactly what the
	// agent wrote — must not report a content change. (The relative form is
	// ambiguous only if a same-basename sibling exists; without one this also
	// pins the plain #557 behavior, and with the eviction fix the absolute
	// key is gone regardless, so no stale hash can be found.)
	t.Chdir(dir)
	if got := tr.validateContentAtEdit("a.go", 200); got != "" {
		t.Fatalf("agent's own write mis-reported as content change (#650): %q", got)
	}
}

// hashFilePrefix must not allocate/read the full 1MB window for small files
// — behavior-wise: hashes of small and >1MB files are stable and distinct
// per content (allocation sizing itself is enforced by review, this pins
// correctness of the sized read path).
func TestIssue650_HashFilePrefixSizedReadCorrectness(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(small, []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	bigBuf := make([]byte, maxHashBytes+512)
	for i := range bigBuf {
		bigBuf[i] = byte(i % 251)
	}
	if err := os.WriteFile(big, bigBuf, 0o644); err != nil {
		t.Fatal(err)
	}

	h1 := hashFilePrefix(small)
	h2 := hashFilePrefix(big)
	if h1 == 0 || h2 == 0 || h1 == h2 {
		t.Fatalf("hashFilePrefix must return distinct non-zero hashes, got %d/%d", h1, h2)
	}
	// Idempotent on re-read.
	if hashFilePrefix(big) != h2 {
		t.Fatal("hashFilePrefix not idempotent on >1MB file")
	}
	// Change beyond the 1MB window is NOT covered (documented trade-off,
	// maxHashBytes window) — change inside the window must flip the hash.
	bigBuf[1024] ^= 0xFF
	if err := os.WriteFile(big, bigBuf, 0o644); err != nil {
		t.Fatal(err)
	}
	if hashFilePrefix(big) == h2 {
		t.Fatal("in-window change not detected by hashFilePrefix")
	}
}
