package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// #1502 case C pin: TestMain's os.Setenv is the only legal form - exempt.
func Test1502TestMainExempt(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "main_test.go")
	src := `package x
import ("os"; "testing")
func TestMain(m *testing.M) { os.Setenv("K", "v"); os.Exit(m.Run()) }
`
	old := checkTestIsolation(tf, "", src)
	if old != "" {
		t.Fatalf("TestMain must be exempt, got %q", old)
	}
}

// #1502 case B pin: cross-file package vars are detected.
func Test1502CrossFileGlobalVar(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "foo.go"), []byte("package x\n\nvar flagVerbose bool\n"), 0o644)
	tf := filepath.Join(dir, "foo_test.go")
	src := `package x
import "testing"
func TestX(t *testing.T) { flagVerbose = true }
`
	if got := checkTestIsolation(tf, "", src); got == "" {
		t.Fatal("cross-file global mutation must be detected")
	}
}

// #1502 case D pin: snapshot+defer restore is hermetic - no warning.
func Test1502SnapshotRestoreClean(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bar.go"), []byte("package y\n\nvar level int\n"), 0o644)
	tf := filepath.Join(dir, "bar_test.go")
	src := `package y
import "testing"
func TestY(t *testing.T) {
	old := level
	level = 5
	defer func() { level = old }
	_ = level
}
`
	if got := checkTestIsolation(tf, "", src); got != "" {
		t.Fatalf("snapshot+restore must not warn, got %q", got)
	}
	// Unrestored write still warns.
	src2 := `package y
import "testing"
func TestY(t *testing.T) { level = 5 }
`
	if got := checkTestIsolation(tf, src, src2); got == "" {
		t.Fatal("unrestored global write must still warn")
	}
}

// #1502 case F pin: swapping pollution kinds is not "fewer violations".
func Test1502KindSwapDetected(t *testing.T) {
	dir := t.TempDir()
	tf := filepath.Join(dir, "k_test.go")
	oldSrc := `package z
import ("os"; "testing")
func TestA(t *testing.T) { os.Setenv("A", "1"); os.Setenv("B", "2"); os.Setenv("C", "3") }
`
	newSrc := `package z
import ("os"; "testing")
func TestA(t *testing.T) { os.Stdout = nil }
`
	if got := checkTestIsolation(tf, oldSrc, newSrc); got == "" {
		t.Fatal("kind swap (3x setenv -> 1x stdio) must still report the new kind")
	}
}
