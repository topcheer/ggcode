package agent

import (
	"os"
	"path/filepath"
	"testing"
)

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

	if h, ok := lookupReadHash(tr, "main.go"); ok {
		t.Fatalf("ambiguous suffix lookup resolved to sibling hash %d (#627)", h)
	}

	if h, ok := lookupReadHash(tr, "svc-a/main.go"); !ok {
		t.Fatal("unique suffix lookup regressed (#557)")
	} else if want := hashFilePrefix(a); h != want {
		t.Fatalf("unique suffix lookup returned wrong hash: got %d want %d", h, want)
	}
}

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

	tr.recordWriteHash("config.go")
	if _, ok := lookupReadHash(tr, a); !ok {
		t.Error("recordWriteHash evicted sibling hash for ambiguous relative form (#627)")
	}
	if _, ok := lookupReadHash(tr, b); !ok {
		t.Error("recordWriteHash evicted sibling hash for ambiguous relative form (#627)")
	}

	tr.recordWriteHash("x/config.go")
	if _, ok := lookupReadHash(tr, a); ok {
		t.Error("recordWriteHash failed to clear the resolved sibling's hash")
	}
	if _, ok := lookupReadHash(tr, b); !ok {
		t.Error("recordWriteHash must not clear the OTHER same-basename file's hash")
	}
}

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
