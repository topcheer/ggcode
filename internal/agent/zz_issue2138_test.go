package agent

// #2138 regression, multi-file leg of #2132: the four multi-file write
// paths persist gofmt-formatted bytes but the integrity loop passed the
// RAW plan.NewContent - every gofmt-touched .go batch write reported a
// fake post-write mismatch. The loop now mirrors write-time formatting
// (mirrorWriteTimeGoFormat, already unit-tested in zz_issue2132_test.go).
// Annex 1: simulateEditFile lacked edit_file's #1676 case 3 CRLF branch,
// so CRLF-file/LF-newtext edits simulated mixed line endings and tripped
// fake mismatches on non-Go files.

import (
	"strings"
	"testing"
)

func TestSimulateEditFileCRLFConversion(t *testing.T) {
	content := "line1\r\nalpha\r\nline3\r\n"
	// CRLF old_text + LF-only new_text: the simulation must convert to
	// CRLF exactly like the real edit_file does (#1676 case 3 mirror).
	got, err := simulateEditFile(content, "alpha\r\n", "beta\n", false)
	if err != nil {
		t.Fatalf("simulateEditFile: %v", err)
	}
	if strings.Contains(got, "beta\n") && !strings.Contains(got, "beta\r\n") {
		t.Fatalf("simulation kept LF new_text on a CRLF file (real tool converts): %q", got)
	}
	want := "line1\r\nbeta\r\nline3\r\n"
	if got != want {
		t.Fatalf("simulated content = %q, want %q", got, want)
	}
	// LF file with LF edit: unchanged behavior (no forced CRLF).
	got2, err := simulateEditFile("a\nalpha\nb\n", "alpha\n", "beta\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "a\nbeta\nb\n" {
		t.Fatalf("LF-file simulation changed: %q", got2)
	}
}

// The multi-file integrity loop applies the same write-time gofmt mirror
// as the single-file leg: verify the mirrored input is what the loop would
// compute (pure-function level - the loop itself is a one-line application
// of this helper, already covered end-to-end by the #2132 tests).
func TestMirrorCoversMultiFileLeg(t *testing.T) {
	raw := "package main\nfunc  main(){println(1)}\n"
	if mirrorWriteTimeGoFormat("batch/x.go", raw) == raw {
		t.Fatal("gofmt-touched .go batch write must be mirrored (raw content would fake-mismatch)")
	}
}
