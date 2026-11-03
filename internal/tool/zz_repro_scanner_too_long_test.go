package tool

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReproScannerTooLongSilentMiss(t *testing.T) {
	dir := t.TempDir()

	var sb strings.Builder
	sb.WriteString("line1 normal\n")
	sb.WriteString(strings.Repeat("a", 100*1024))
	sb.WriteString("\n")
	sb.WriteString("TARGET_MATCH_HERE\n")
	sb.WriteString("line4 normal\n")
	path := filepath.Join(dir, "bundle.js")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	re := regexp.MustCompile(`TARGET_MATCH_HERE`)

	if !grepFileHasMatch(path, re) {
		t.Errorf("grepFileHasMatch: missed match after >64KB line (silent ErrTooLong)")
	}
	if got := grepFileCount(path, re); got != 1 {
		t.Errorf("grepFileCount: got %d, want 1 (silent miss after >64KB line)", got)
	}
	matches := grepFileContent(path, re, grepArgs{})
	if len(matches) != 1 {
		t.Errorf("grepFileContent: got %d matches, want 1 (silent miss after >64KB line)", len(matches))
	}

	var sb2 strings.Builder
	sb2.WriteString("line1 normal\nTARGET_MATCH_HERE\nline4 normal\n")
	path2 := filepath.Join(dir, "normal.js")
	if err := os.WriteFile(path2, []byte(sb2.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if !grepFileHasMatch(path2, re) {
		t.Fatal("control file: expected match but got none — test setup broken")
	}
}
