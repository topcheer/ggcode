package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #4435: three dirty-channel fixes -
// A) skip-dir filter (vendor/node_modules must not enter the corpus),
// B) only os.IsNotExist removes a doc (stat errors keep it),
// C) zero-term files must not enter the corpus (matches doBuild/loadDiskCache).

func issue4435DocCount(m *CodeIndexManager) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.index.docs)
}

func issue4435HasDoc(m *CodeIndexManager, rel string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.index.docs {
		if d.path == rel {
			return true
		}
	}
	return false
}

// Bug A: a .go file under a skip dir (vendor) must not be indexed even though
// write_file passes its path verbatim into MarkDirty.
func TestIssue4435DirtyChannelSkipsSkipDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package p\nvar anchor int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	vend := filepath.Join(dir, "vendor", "lib", "dep.go")
	if err := os.WriteFile(vend, []byte("package lib\nvar vendored int\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.MarkDirty([]string{vend})
	m.rebuildDirty("test")

	if issue4435HasDoc(m, "vendor/lib/dep.go") {
		t.Fatal("vendor .go file entered the corpus via dirty channel (no skip-dir filter)")
	}
	if !issue4435HasDoc(m, "root.go") {
		t.Fatal("root.go disappeared from the corpus")
	}
}

// Bug C: a file whose content tokenizes to zero terms must not enter the
// corpus (loadDiskCache drops empty-tf docs, so indexing them makes build and
// load disagree).
func TestIssue4435DirtyChannelSkipsZeroTermFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package p\nvar keepme int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())
	before := issue4435DocCount(m)

	empty := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(empty, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{empty})
	m.rebuildDirty("test")

	if got := issue4435DocCount(m); got != before {
		t.Fatalf("zero-term doc entered the corpus: docs %d -> %d", before, got)
	}
}

// Bug B regression pair: deletion still removes the doc; a stat error other
// than NotExist (achieved via a path with a NUL-free impossible parent - a
// regular file where a directory is required) must KEEP the doc.
func TestIssue4435DirtyChannelStatErrorKeepsDoc(t *testing.T) {
	dir := t.TempDir()
	code := filepath.Join(dir, "code.go")
	if err := os.WriteFile(code, []byte("package p\nvar keepme int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())
	if !issue4435HasDoc(m, "code.go") {
		t.Fatal("code.go not indexed after build")
	}

	// ENOTDIR: create a regular file where the dirty path expects a directory
	// component. os.Stat returns ENOTDIR (not IsNotExist) - the doc must stay.
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "blocker", "nested", "code.go")
	m.MarkDirty([]string{blocked})
	m.rebuildDirty("test")
	if !issue4435HasDoc(m, "code.go") {
		t.Fatal("unrelated stat error evicted an existing doc")
	}

	// Real deletion (IsNotExist) still removes.
	if err := os.Remove(code); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{code})
	m.rebuildDirty("test")
	if issue4435HasDoc(m, "code.go") {
		t.Fatal("deleted file still present in index after dirty rebuild")
	}
}
