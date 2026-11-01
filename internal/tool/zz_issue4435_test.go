package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// #4435 bug 1: a skip-dir file (vendor/) written via the dirty channel must
// NOT enter the corpus - the full scan (isSkipDir) and cache load
// (isInSkipDir) exclude those directories; the dirty channel must too.
func TestIssue4435_DirtyChannelFiltersSkipDirs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "code.go"), []byte("package p\nvar keepme int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	before := len(m.index.docs)
	m.mu.RUnlock()

	vendor := filepath.Join(dir, "vendor", "lib", "dep.go")
	if err := os.MkdirAll(filepath.Dir(vendor), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vendor, []byte("package lib\nvar vendorterms string\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{vendor})
	m.rebuildDirty("test")

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.index.docs) != before {
		t.Fatalf("vendor file entered the corpus via dirty channel: docs %d -> %d", before, len(m.index.docs))
	}
}

// #4435 bug 2: only os.IsNotExist removes a doc - a permission/IO error on a
// REAL file must keep it indexed (was: any Stat error removed it).
func TestIssue4435_StatErrorKeepsDoc(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "code.go")
	if err := os.WriteFile(src, []byte("package p\nvar keepme int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	before := len(m.index.docs)
	m.mu.RUnlock()

	// Make the file stat-inaccessible via a non-NotExists error: remove the
	// file and recreate a PARENT path component as a file so Stat hits ENOTDIR
	// (not IsNotExist on all platforms). Simpler portable route: chmod 000 the
	// file (EACCES on read, stat still ok) is not enough - use a symlink loop.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(src, src); err != nil {
		t.Fatal(err) // self-symlink: Stat -> ELOOP (non-NotExists)
	}
	m.MarkDirty([]string{src})
	m.rebuildDirty("test")

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.index.docs) != before {
		t.Fatalf("non-NotExists stat error must keep the doc indexed: docs %d -> %d", before, len(m.index.docs))
	}
}

// #4435 bug 3: a file overwritten with untokenizable content must not leave
// an empty-TF doc (append) or a stale full-TF corpse (replace) in the corpus.
func TestIssue4435_EmptyTermsDocRemoved(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "code.go")
	if err := os.WriteFile(src, []byte("package p\nvar realterms int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewCodeIndexManager(dir)
	defer m.Stop()
	m.doBuild(context.Background())

	m.mu.RLock()
	before := len(m.index.docs)
	m.mu.RUnlock()
	if before != 1 {
		t.Fatalf("seed corpus: want 1 doc, got %d", before)
	}
	// Overwrite with content that tokenizes to zero terms.
	if err := os.WriteFile(src, []byte("////\n\n////\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{src})
	m.rebuildDirty("test")

	m.mu.RLock()
	afterReplace := len(m.index.docs)
	m.mu.RUnlock()
	if afterReplace != 0 {
		t.Fatalf("empty-terms doc must be dropped on replace: docs %d -> %d", before, afterReplace)
	}

	// And the append path must not add a fresh empty-TF doc either.
	empty2 := filepath.Join(dir, "empty2.go")
	if err := os.WriteFile(empty2, []byte("////\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.MarkDirty([]string{empty2})
	m.rebuildDirty("test")
	m.mu.RLock()
	final := len(m.index.docs)
	m.mu.RUnlock()
	if final != 0 {
		t.Fatalf("empty-terms file must not be appended: docs %d", final)
	}
}
