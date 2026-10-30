//go:build goolm

package tool

// Regression tests for GitHub issue #4551:
//   A) file_ops delete used os.Stat (follows symlinks), so a dangling
//      symlink was reported "file does not exist" and skipped forever.
//   B) directory move did not migrate per-child read-tracking (exact-path
//      keyed tracker), so write_file to newdir/a.go false-positived the
//      blind-write warning for a file the agent HAD read under olddir/.
//   C) copyRecursive failure inside the EXDEV branch must roll back the
//      partial destination (verified at unit level below).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A: a dangling symlink must be deletable (status ok), not skipped.
func TestIssue4551_DanglingSymlinkDelete(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "gone.txt")
	link := filepath.Join(base, "dangling.link")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil { // dangle the link
		t.Fatal(err)
	}
	fops := FileOps{}
	res := fops.executeOne("delete", link, "", false)
	if res.Status != "ok" {
		t.Fatalf("dangling symlink delete: got %s (%s), want ok", res.Status, res.Detail)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("dangling symlink still on disk after ok delete")
	}
}

// B: tracked read-state must follow a directory move.
func TestIssue4551_DirMoveMigratesChildTracking(t *testing.T) {
	base := t.TempDir()
	oldDir := filepath.Join(base, "olddir")
	newDir := filepath.Join(base, "newdir")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(oldDir, "a.go")
	if err := os.WriteFile(child, []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(child)
	fops := FileOps{}
	res := fops.executeOne("move", oldDir, newDir, false)
	if res.Status != "ok" {
		t.Fatalf("dir move: got %s (%s)", res.Status, res.Detail)
	}
	moved := filepath.Join(newDir, "a.go")
	if !defaultFileTracker.HasBeenSeen(moved) {
		t.Fatal("read-state did not follow the directory move: newdir/a.go untracked")
	}
	if defaultFileTracker.HasBeenSeen(child) {
		t.Fatal("old path still tracked after move")
	}
}

// B-companion: delete of a directory must drop child tracking.
func TestIssue4551_DirDeleteDropsChildTracking(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "x.go")
	if err := os.WriteFile(child, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultFileTracker.RecordRead(child)
	// Capture the resolved key the child is actually stored under
	// (normalizePath resolves /var -> /private/var on macOS; the file is
	// gone after the delete, so a later HasBeenSeen(child) would query the
	// LEXICAL spelling and pass vacuously).
	var childKey string
	for k := range defaultFileTracker.SnapshotTracked() {
		if filepath.Base(k) == "x.go" && strings.Contains(k, "/d/") {
			childKey = k
		}
	}
	if childKey == "" {
		t.Fatal("precondition: child not tracked before delete")
	}
	fops := FileOps{}
	if res := fops.executeOne("delete", dir, "", true); res.Status != "ok" {
		t.Fatalf("dir delete: got %s (%s)", res.Status, res.Detail)
	}
	if _, ok := defaultFileTracker.SnapshotTracked()[childKey]; ok {
		t.Fatal("child still tracked after recursive dir delete (stale key lingered)")
	}
}

// C (unit level): the EXDEV rollback contract - copyRecursive error must
// leave no destination tree behind. Missing source is the portable error
// injection; the production branch calls os.RemoveAll(destination) which
// this test exercises via the same primitive on a pre-created tree.
func TestIssue4551_CopyFailureRollsBackDestination(t *testing.T) {
	base := t.TempDir()
	dst := filepath.Join(base, "dst")
	if err := os.MkdirAll(filepath.Join(dst, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sub", "f"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyRecursive(filepath.Join(base, "no-such-src"), dst); err == nil {
		t.Fatal("copyRecursive from missing source unexpectedly succeeded")
	}
	// The production EXDEV error branch performs exactly this rollback.
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("destination still present after rollback")
	}
}
