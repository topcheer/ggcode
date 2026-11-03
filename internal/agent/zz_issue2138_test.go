package agent

import (
	"strings"
	"testing"
)

func TestSimulateEditFileCRLFConversion(t *testing.T) {
	content := "line1\r\nalpha\r\nline3\r\n"

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

	got2, err := simulateEditFile("a\nalpha\nb\n", "alpha\n", "beta\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "a\nbeta\nb\n" {
		t.Fatalf("LF-file simulation changed: %q", got2)
	}
}

func TestMirrorCoversMultiFileLeg(t *testing.T) {
	raw := "package main\nfunc  main(){println(1)}\n"
	if mirrorWriteTimeGoFormat("batch/x.go", raw) == raw {
		t.Fatal("gofmt-touched .go batch write must be mirrored (raw content would fake-mismatch)")
	}
}
