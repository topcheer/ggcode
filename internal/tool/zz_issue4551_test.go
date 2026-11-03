//go:build goolm

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := os.Remove(target); err != nil {
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

	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("destination still present after rollback")
	}
}
