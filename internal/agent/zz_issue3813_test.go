//go:build goolm

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #3813: a linter exiting non-zero while extractLintWarnings parses ZERO
// warnings used to be indistinguishable from a clean run - the failure
// (including timeouts and linter crashes) was swallowed whole. The fix
// synthesizes one warning carrying the exit status and a raw-output tail.

func fakeLinterDir(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-lint")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Makefile", "lint:\n\t"+bin+"\n")
	return dir
}

func TestIssue3813_UnrecognizedFailureSynthesizesWarning(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make unavailable")
	}
	dir := fakeLinterDir(t, "echo 'weird tool crash output'\nexit 1\n")
	a := NewAgent(nil, nil, "sys", 5)
	res := a.runLintCheck(context.Background(), dir)
	if res == nil {
		t.Fatal("expected a lint result")
	}
	if res.Passed {
		t.Fatal("non-zero exit must not pass")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("unrecognized non-zero lint failure must synthesize a warning, not be swallowed")
	}
	if !strings.Contains(res.Warnings[0], "raw output tail") || !strings.Contains(res.Warnings[0], "weird tool crash output") {
		t.Fatalf("synthesized warning must carry the raw tail, got: %s", res.Warnings[0])
	}
}

func TestIssue3813_CleanRunStillClean(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make unavailable")
	}
	dir := fakeLinterDir(t, "exit 0\n")
	a := NewAgent(nil, nil, "sys", 5)
	res := a.runLintCheck(context.Background(), dir)
	if res == nil {
		t.Fatal("expected a lint result")
	}
	if !res.Passed || len(res.Warnings) != 0 {
		t.Fatalf("zero-exit zero-warning run must stay clean, got %+v", res)
	}
}
