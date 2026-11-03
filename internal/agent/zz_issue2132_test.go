package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeCollapsesPluralLineRefs(t *testing.T) {
	a := "lines 3, 7: mixed tabs and spaces (2 occurrences)"
	b := "lines 4, 8: mixed tabs and spaces (2 occurrences)"
	if normalizeIntegrityMsg(a) != normalizeIntegrityMsg(b) {
		t.Fatal("plural line-ref shifts must normalize identically (P1: they surfaced as fake new problems)")
	}

	c := "lines 3, 7: mixed tabs and spaces"
	d := "lines 4, 8, 12: mixed tabs and spaces"
	if normalizeIntegrityMsg(c) != normalizeIntegrityMsg(d) {
		t.Fatal("list-length drift under line shift must collapse (position info)")
	}

	e := "(2 occurrences)"
	f := "(3 occurrences)"
	if normalizeIntegrityMsg(e) == normalizeIntegrityMsg(f) {
		t.Fatal("occurrence counts must survive normalization (#605 G4)")
	}
}

func TestMirrorWriteTimeGoFormat(t *testing.T) {
	raw := "package main\nfunc  main(){println(1)}\n"
	mirrored := mirrorWriteTimeGoFormat("x.go", raw)
	if mirrored == raw {
		t.Fatal("unformatted Go content must be mirrored to its gofmt form")
	}
	if mirrorWriteTimeGoFormat("x.py", raw) != raw {
		t.Fatal("non-Go paths pass through unchanged")
	}
	if mirrorWriteTimeGoFormat("x.go", "not valid go {") != "not valid go {" {
		t.Fatal("unparseable content passes through (same fallback as the tools)")
	}
}

func TestMismatchStillRunsChecks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.py")

	disk := "def f():\n\treturn 1\n \tx = 2\n"
	if err := os.WriteFile(p, []byte(disk), 0o644); err != nil {
		t.Fatal(err)
	}
	got := checkWriteIntegrity(p, "", "different planned content")
	if got == "" {
		t.Fatal("mismatch path must still report (was: silent skip)")
	}
	if !strings.Contains(got, "post-write mismatch") {
		t.Fatalf("mismatch notice missing: %q", got)
	}
}
