//go:build goolm

package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3825_PhonyDoesNotCountAsTarget(t *testing.T) {
	dir := t.TempDir()
	mk := filepath.Join(dir, "Makefile")
	content := "include core.mk\n.PHONY: build clean\n.DEFAULT_GOAL := build\n"
	if err := os.WriteFile(mk, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := makefileTargets(mk)
	if len(targets) != 0 {
		t.Fatalf("dot-directives must not register as targets, got %v", targets)
	}

	if err := os.WriteFile(mk, []byte("build:\n\tgo build ./...\nclean:\n\trm -rf bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets = makefileTargets(mk)
	if !targets["build"] || !targets["clean"] {
		t.Fatalf("real targets must register, got %v", targets)
	}
}

func TestIssue3825_IncludeOnlyDisablesTargetCheck(t *testing.T) {
	dir := t.TempDir()
	mk := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(mk, []byte("include core.mk\n.PHONY: build\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(makefileTargets(mk)); got != 0 {
		t.Fatalf("include-only root must yield zero targets (check disabled), got %d", got)
	}
}
