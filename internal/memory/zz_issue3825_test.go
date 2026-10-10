package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIssue3825PhonyDoesNotEnableMakefileGate: an include-only Makefile whose
// only colon lines are special directives (.PHONY:) must NOT enable the
// make-target drift gate -- otherwise every fenced `make X` referencing the
// real targets (defined in the included .mk) is a false positive (#3049-C2
// regression path, #3825).
func TestIssue3825PhonyDoesNotEnableMakefileGate(t *testing.T) {
	dir := t.TempDir()
	mk := "include core.mk\n.PHONY: build clean\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "build via:\n```\nmake build\n```\n"
	findings := ScanConventionDrift(content, dir)
	for _, f := range findings {
		if strings.Contains(f, "not defined in Makefile") {
			t.Fatalf("false positive with include-only + .PHONY Makefile: %q", f)
		}
	}
}

// TestIssue3825SpecialDirectivesNotRegistered: .PHONY/.DEFAULT_GOAL/%.o lines
// must not be registered as targets themselves.
func TestIssue3825SpecialDirectivesNotRegistered(t *testing.T) {
	dir := t.TempDir()
	mk := ".PHONY: build\n.DEFAULT_GOAL: build\n%.o: %.c\n\tcc $< -o $@\nbuild:\n\tgo build ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
		t.Fatal(err)
	}
	got := makefileTargets(filepath.Join(dir, "Makefile"))
	for _, spurious := range []string{".PHONY", ".DEFAULT_GOAL", "%.o"} {
		if got[spurious] {
			t.Errorf("special directive %q registered as target: %v", spurious, got)
		}
	}
	if !got["build"] {
		t.Errorf("real target build missing: %v", got)
	}
}

// TestIssue3825UndefinedTargetStillFlagged: the normal drift check (undefined
// target in a Makefile that genuinely defines targets) keeps working.
func TestIssue3825UndefinedTargetStillFlagged(t *testing.T) {
	dir := t.TempDir()
	mk := ".PHONY: build\nbuild:\n\tgo build ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(mk), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "```\nmake nosuchtarget\n```\n"
	findings := ScanConventionDrift(content, dir)
	found := false
	for _, f := range findings {
		if strings.Contains(f, `"nosuchtarget"`) && strings.Contains(f, "not defined in Makefile") {
			found = true
		}
	}
	if !found {
		t.Fatalf("undefined-target finding missing: %v", findings)
	}
}
