package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDriftFixture(t *testing.T, makefile string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if makefile != "" {
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanConventionDriftCleanProject(t *testing.T) {
	dir := writeDriftFixture(t, "build:\n\techo hi\n\nverify-ci:\n\tgo test ./...\n", map[string]string{
		"scripts/run.sh": "#!/bin/sh\n",
	})
	content := "## Build\n\n```\nmake build\nmake verify-ci\nbash scripts/run.sh\n```\n"
	if d := ScanConventionDrift(content, dir); len(d) != 0 {
		t.Fatalf("all claims valid, want no drift, got %v", d)
	}
}

func TestScanConventionDriftStaleMakeTarget(t *testing.T) {
	dir := writeDriftFixture(t, "build:\n\techo hi\n", nil)
	content := "```\nmake build\nmake removed-target\n```\n"
	d := ScanConventionDrift(content, dir)
	if len(d) != 1 || !strings.Contains(d[0], "removed-target") {
		t.Fatalf("want exactly the stale make target flagged, got %v", d)
	}
}

func TestScanConventionDriftMissingScript(t *testing.T) {
	dir := writeDriftFixture(t, "", nil) // no Makefile: make checks disabled
	content := "```\nmake anything\nbash scripts/gone.sh\n```\n"
	d := ScanConventionDrift(content, dir)
	if len(d) != 1 || !strings.Contains(d[0], "scripts/gone.sh") {
		t.Fatalf("want exactly the missing script flagged (make skipped, no Makefile), got %v", d)
	}
}

// Inline prose must never be flagged — only fenced blocks are claims.
func TestScanConventionDriftIgnoresInlineProse(t *testing.T) {
	dir := writeDriftFixture(t, "build:\n\techo hi\n", nil)
	content := "Run `make totally-fake` and `bash nope.sh` sometime.\n"
	if d := ScanConventionDrift(content, dir); len(d) != 0 {
		t.Fatalf("inline prose must not be flagged, got %v", d)
	}
}

// LoadProjectMemory must append the drift warning when the project's own
// memory file carries a stale claim.
func TestLoadProjectMemoryAppendsDriftWarning(t *testing.T) {
	dir := writeDriftFixture(t, "build:\n\techo hi\n", map[string]string{
		"AGENTS.md": "# Guide\n\n```\nmake build\nmake stale-target\n```\n",
	})
	content, _, err := LoadProjectMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "[convention-drift]") || !strings.Contains(content, "stale-target") {
		t.Fatalf("expected drift warning appended to loaded memory, got: %s", content)
	}
	if !strings.Contains(content, "make build") {
		t.Fatal("original memory content must be preserved alongside the warning")
	}
}

func TestScanConventionDriftCapAndDedup(t *testing.T) {
	dir := writeDriftFixture(t, "a:\n", nil)
	var block strings.Builder
	block.WriteString("```\n")
	for i := 0; i < 8; i++ {
		block.WriteString("make missing")
		block.WriteString(strings.Repeat("-x", i+1))
		block.WriteString("\n")
	}
	block.WriteString("```\n")
	d := ScanConventionDrift(block.String(), dir)
	if len(d) != conventionDriftCap {
		t.Fatalf("findings must cap at %d, got %d", conventionDriftCap, len(d))
	}
}
