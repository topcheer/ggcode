package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGrepScannerTooLongVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.js")

	content := strings.Repeat("x", 70*1024) + "\nneedle here\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	re := regexp.MustCompile(`needle`)

	ctrlPath := filepath.Join(dir, "ctrl.js")
	if err := os.WriteFile(ctrlPath, []byte("hello\nneedle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !grepFileHasMatch(ctrlPath, re) {
		t.Fatal("control: expected match in ctrl file")
	}
	if grepFileCount(ctrlPath, re) != 1 {
		t.Fatalf("control count: got %d want 1", grepFileCount(ctrlPath, re))
	}

	t.Logf("hasMatch(oversized) = %v", grepFileHasMatch(path, re))
	t.Logf("count(oversized) = %d", grepFileCount(path, re))

	bigMatch := "prefix " + strings.Repeat("y", 70*1024) + " needle-end"
	bigPath := filepath.Join(dir, "bigmatch.js")
	if err := os.WriteFile(bigPath, []byte(bigMatch), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("hasMatch(match inside oversized line) = %v", grepFileHasMatch(bigPath, re))
	t.Logf("count(match inside oversized line) = %d", grepFileCount(bigPath, re))

	matches := grepFileContent(path, re, grepArgs{})
	t.Logf("grepFileContent(oversized) returns %d matches", len(matches))
}
