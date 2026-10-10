//go:build goolm

package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// #3825: `.PHONY` (and other dot-directive targets) must not register as
// Makefile targets. An include-only root Makefile (`include core.mk` +
// `.PHONY: build`) used to produce a non-empty map holding ".PHONY", so
// the #3049-C2 empty-map guard never fired and every target referenced
// from fenced blocks false-flagged as undefined.
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

	// Real targets still register.
	if err := os.WriteFile(mk, []byte("build:\n\tgo build ./...\nclean:\n\trm -rf bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets = makefileTargets(mk)
	if !targets["build"] || !targets["clean"] {
		t.Fatalf("real targets must register, got %v", targets)
	}
}

// The downstream contract: an include-only Makefile (real targets live in
// the included .mk) now behaves like the #3049-C2 case again - the target
// map is empty, so target verification is disabled instead of flagging.
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
