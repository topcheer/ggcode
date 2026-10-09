package agent

// #3620 probes: package-level test semantics. A file with no same-name
// _test.go sibling is NOT untested when other *_test.go in the directory
// reference its exports directly (the repo's zz_issueNNNN_test.go pattern)
// or name them in Test conventions. A directory with no test files at all
// keeps reporting full untested coverage.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3620_DirLevelSiblingCoverage(t *testing.T) {
	dir := t.TempDir()
	// Source file, no same-name sibling.
	src := filepath.Join(dir, "playbook_failures.go")
	os.WriteFile(src, []byte("package agent\n\nfunc RecordFailureAttribution() {}\n\nfunc FailureHintsForPrompt() {}\n"), 0o644)
	// Cross-file test referencing exports directly.
	os.WriteFile(filepath.Join(dir, "zz_issue1_test.go"), []byte("package agent\n\nimport \"testing\"\n\nfunc TestCross(t *testing.T) { RecordFailureAttribution() }\n"), 0o644)

	if !hasGoTestFile(dir, src) {
		t.Fatal("hasGoTestFile must accept any *_test.go in the directory")
	}
	gaps := untestedExportedFuncs(dir, "playbook_failures.go")
	for _, g := range gaps {
		if g == "RecordFailureAttribution" {
			t.Fatal("directly-referenced export must not be untested")
		}
	}
}

func TestIssue3620_NoTestDirStillFullGap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "lonely.go")
	os.WriteFile(src, []byte("package agent\n\nfunc Exported() {}\n"), 0o644)

	if hasGoTestFile(dir, src) {
		t.Fatal("directory without any _test.go must report no coverage")
	}
	gaps := untestedExportedFuncs(dir, "lonely.go")
	if len(gaps) != 1 || gaps[0] != "Exported" {
		t.Fatalf("true no-coverage directory must list all exports, got %v", gaps)
	}
}

func TestIssue3620_ConventionMatchAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "helper.go")
	os.WriteFile(src, []byte("package agent\n\nfunc Widget() {}\n"), 0o644)
	// No direct reference, but a cross-file Test name follows convention.
	os.WriteFile(filepath.Join(dir, "other_test.go"), []byte("package agent\n\nimport \"testing\"\n\nfunc TestWidget(t *testing.T) {}\n"), 0o644)

	gaps := untestedExportedFuncs(dir, "helper.go")
	if len(gaps) != 0 {
		t.Fatalf("TestWidget convention match must cover Widget, got %v", gaps)
	}
}

func TestIssue3620_WordBoundaryNoPrefixHit(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "prefix.go")
	os.WriteFile(src, []byte("package agent\n\nfunc Widget() {}\n"), 0o644)
	// Reference contains Widget only as a substring of a longer identifier.
	os.WriteFile(filepath.Join(dir, "zz_test.go"), []byte("package agent\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { _ = WidgetExtended }\n"), 0o644)

	gaps := untestedExportedFuncs(dir, "prefix.go")
	if len(gaps) != 1 || gaps[0] != "Widget" {
		t.Fatalf("substring inside a longer identifier must not count as a reference, got %v", gaps)
	}
}
