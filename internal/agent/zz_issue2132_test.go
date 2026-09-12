package agent

// #2132 regression:
//   - P1: normalizeIntegrityMsg collapsed only SINGULAR "line N" refs
//     (#2125); the Python indent check emits PLURAL "lines 3, 7:" lists,
//     which escaped normalization and surfaced as fake new problems on any
//     line shift.
//   - P2: a post-write mismatch early-returned and silently skipped EVERY
//     registered check; and because write_file persists gofmt-formatted
//     bytes while the pipeline compared the RAW argument, every
//     gofmt-touched Go write mismatched - the imperfect writes most in
//     need of checking were systematically exempt (plus a double report).

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
	// Per the issue's fix suggestion the WHOLE list collapses into one
	// placeholder: list membership is position-dependent (lines shift
	// in/out of the range with content movement), so a shorter/longer list
	// on shifted content must not surface as a new problem either.
	c := "lines 3, 7: mixed tabs and spaces"
	d := "lines 4, 8, 12: mixed tabs and spaces"
	if normalizeIntegrityMsg(c) != normalizeIntegrityMsg(d) {
		t.Fatal("list-length drift under line shift must collapse (position info)")
	}
	// But the magnitude COUNT suffix still matters (#605 G4 contract).
	e := "(2 occurrences)"
	f := "(3 occurrences)"
	if normalizeIntegrityMsg(e) == normalizeIntegrityMsg(f) {
		t.Fatal("occurrence counts must survive normalization (#605 G4)")
	}
}

// P2 root cause: the integrity pipeline must compare against the
// gofmt-formatted bytes the write tools persist, not the raw argument.
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

// P2: on a REAL mismatch the registered checks still run against the disk
// bytes and the mismatch is reported once (merged), not as a skip.
func TestMismatchStillRunsChecks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.py")
	// Disk holds a tab/space mixture the "planned" content lacks: the
	// mismatch must not silence the indent finding.
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
