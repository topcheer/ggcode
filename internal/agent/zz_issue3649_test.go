package agent

// #3649 probes: (1) sibling test files using table-driven naming
// (TestFoo_tableDriven) must not misreport Foo as untested; (2) a
// parse-failed sibling must suppress (not escalate) untested reporting.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIssue3649Files(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIssue3649_SiblingTableDrivenVariantNotUntested(t *testing.T) {
	dir := t.TempDir()
	writeIssue3649Files(t, dir, map[string]string{
		"foo.go":      "package pkg\n\nfunc ParseConfig(s string) int { return len(s) }\n",
		"foo_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestParseConfig_tableDriven(t *testing.T) {\n\t_ = ParseConfig(\"x\")\n}\n",
	})
	got := untestedExportedFuncs(dir, "foo.go")
	for _, name := range got {
		if name == "ParseConfig" {
			t.Fatal("ParseConfig is covered by table-driven variant TestParseConfig_tableDriven but reported untested")
		}
	}
}

func TestIssue3649_SiblingParseFailedSuppressesMisreport(t *testing.T) {
	dir := t.TempDir()
	writeIssue3649Files(t, dir, map[string]string{
		"foo.go": "package pkg\n\nfunc Foo() int { return 1 }\n",
		// Broken mid-edit WIP sibling: parse fails.
		"foo_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestFoo(t *testing.T) { this is not valid go",
	})
	got := untestedExportedFuncs(dir, "foo.go")
	for _, name := range got {
		if name == "Foo" {
			t.Fatal("parse-failed sibling must suppress untested reporting, not escalate to false positive")
		}
	}
}

func TestIssue3649_GenuineUntestedStillReported(t *testing.T) {
	dir := t.TempDir()
	writeIssue3649Files(t, dir, map[string]string{
		"foo.go":      "package pkg\n\nfunc Bar() int { return 2 }\n",
		"foo_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) {}\n",
	})
	got := untestedExportedFuncs(dir, "foo.go")
	found := false
	for _, name := range got {
		if name == "Bar" {
			found = true
		}
	}
	if !found {
		t.Fatal("genuinely untested Bar must still be reported (guard against over-suppression)")
	}
}
