package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2268/#2269 bundle pins.

// #2269: the streaming range reader must surface the conflict warning on
// RAW lines - the caller-side guard on line-numbered text never fired.
func TestIssue2269StreamingConflictGuardFires(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.go")
	var sb strings.Builder
	sb.WriteString("package main\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("// filler line to grow the file past trivial size\n")
	}
	sb.WriteString("<<<<<<< HEAD\n")
	sb.WriteString("ours\n")
	sb.WriteString("=======\n")
	sb.WriteString("theirs\n")
	sb.WriteString(">>>>>>> dev\n")
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readFileRangeStreaming(p, 40, 20, readFileRangeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[WARNING]") || !strings.Contains(got, "conflict") {
		t.Errorf("streaming read with conflict markers must carry the warning, got tail: %q", tailN(got, 200))
	}
	// sanity: the marker line itself is still visible in the range
	if !strings.Contains(got, "<<<<<<< HEAD") {
		t.Error("marker line should still be visible in output")
	}
}

func TestIssue2269CleanRangeNoWarning(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc f() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readFileRangeStreaming(p, 1, 10, readFileRangeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "[WARNING]") {
		t.Error("clean file must not carry a conflict warning")
	}
}

// #2269 R100: a LONE ======= (setext underline) or >>>>>>> outside a
// conflict region must NOT fire - the first cut fired on all three
// markers independently (review sa-211 false positive).
func TestIssue2269LoneMarkersNoWarning(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.md")
	body := "Title\n=======\n\nsome text\n\nquoted >>>>>>> arrow\n\nmore\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readFileRangeStreaming(p, 1, 20, readFileRangeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "[WARNING]") {
		t.Error("lone ======= / >>>>>>> outside a region must stay quiet")
	}
}

func tailN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
