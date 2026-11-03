package agent

import (
	"os"
	"path/filepath"
	"testing"
)

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

	rel := filepath.Join("svc-a", "main.go")
	tr := newReadHashTracker()
	tr.recordReadHash(a)
	tr.recordReadHash(rel)
	tr.recordReadHash(b)

	tr.recordWriteHash(a)

	if _, ok := lookupReadHash(tr, rel); ok {
		t.Fatal("recordWriteHash left the same file's other-form key behind under sibling ambiguity (#650)")
	}
	if _, ok := lookupReadHash(tr, a); ok {
		t.Fatal("recordWriteHash left the exact key behind")
	}

	if _, ok := lookupReadHash(tr, b); !ok {
		t.Fatal("recordWriteHash evicted the same-basename sibling's hash (#627 regression)")
	}
}

func TestIssue650_NoSelfEditFalsePositiveAfterWrite(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := newReadHashTracker()
	tr.recordReadHash(f)
	tr.recordWriteHash(f)

	t.Chdir(dir)
	if got := tr.validateContentAtEdit("a.go", 200); got != "" {
		t.Fatalf("agent's own write mis-reported as content change (#650): %q", got)
	}
}

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

	if hashFilePrefix(big) != h2 {
		t.Fatal("hashFilePrefix not idempotent on >1MB file")
	}

	bigBuf[1024] ^= 0xFF
	if err := os.WriteFile(big, bigBuf, 0o644); err != nil {
		t.Fatal(err)
	}
	if hashFilePrefix(big) == h2 {
		t.Fatal("in-window change not detected by hashFilePrefix")
	}
}
