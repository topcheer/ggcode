package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthReportBasic(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	// Create entries of different categories.
	am.SaveMemory("build-process", "short persistent note") // persistent
	am.SaveMemory("competitor-analysis", "research entry")  // evolving
	am.SaveMemory("impl-task-test", "transient note")       // transient

	report := am.HealthReport("")

	if report.Total != 3 {
		t.Errorf("expected total 3, got %d", report.Total)
	}
	if report.Active != 3 {
		t.Errorf("expected active 3, got %d", report.Active)
	}
	if report.Persistent != 1 {
		t.Errorf("expected 1 persistent, got %d", report.Persistent)
	}
	if report.Evolving != 1 {
		t.Errorf("expected 1 evolving, got %d", report.Evolving)
	}
	if report.Transient != 1 {
		t.Errorf("expected 1 transient, got %d", report.Transient)
	}
}

func TestHealthReportFormat(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	am.SaveMemory("build-process", "short note")
	am.SaveMemory("api-gotcha", "another note")

	report := am.HealthReport("")
	output := report.FormatHealthReport()

	if !strings.Contains(output, "Memory Health:") {
		t.Errorf("expected 'Memory Health:' in output, got: %s", output)
	}
	if !strings.Contains(output, "Categories:") {
		t.Errorf("expected 'Categories:' in output")
	}
	if !strings.Contains(output, "Status:") {
		t.Errorf("expected 'Status:' in output")
	}
}

func TestHealthReportEmpty(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	report := am.HealthReport("")
	if report.Total != 0 {
		t.Errorf("expected 0 total for empty dir, got %d", report.Total)
	}
	if report.Active != 0 {
		t.Errorf("expected 0 active for empty dir, got %d", report.Active)
	}
}

func TestHealthReportStaleness(t *testing.T) {
	dir := t.TempDir()
	workingDir := t.TempDir()

	am := &AutoMemory{dir: dir}
	// Create an entry with a broken path reference.
	am.SaveMemory("build-process-impl", "See internal/nonexistent/missing.go for details")

	report := am.HealthReport(workingDir)
	if report.StaleBrokenPaths == 0 {
		t.Error("expected broken path detection in health report")
	}

	output := report.FormatHealthReport()
	if !strings.Contains(output, "[STALE]") {
		t.Errorf("expected [STALE] in output, got: %s", output)
	}
}

func TestHealthReportBrokenSymbols(t *testing.T) {
	dir := t.TempDir()
	workingDir := t.TempDir()

	// Workspace lacks the referenced symbol entirely.
	am := &AutoMemory{dir: dir}
	am.SaveMemory("probe-stale", "Call `VanishedHelperFunc` before saving.")

	report := am.HealthReport(workingDir)
	if report.StaleBrokenSymbols != 1 {
		t.Fatalf("expected 1 stale broken symbol, got %d", report.StaleBrokenSymbols)
	}
	output := report.FormatHealthReport()
	if !strings.Contains(output, "code symbols absent from the workspace") {
		t.Errorf("expected stale symbol warning in formatted report, got: %s", output)
	}
}

func TestHealthReportProbeTruncated(t *testing.T) {
	// #2621: truncation must surface in the report as a downgrade note,
	// never as broken-symbol warnings.
	workingDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workingDir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "a", "filler.go"), []byte("package a\nfunc fillerThing() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	am := &AutoMemory{dir: t.TempDir()}
	am.SaveMemory("probe-cap", "Always call `realSymbolName` before exit.")

	oldBytes := maxProbeTotalBytes
	maxProbeTotalBytes = 1
	defer func() { maxProbeTotalBytes = oldBytes }()

	report := am.HealthReport(workingDir)
	if !report.StaleProbeTruncated {
		t.Fatal("expected StaleProbeTruncated wired from the staleness scan")
	}
	output := report.FormatHealthReport()
	if !strings.Contains(output, "truncated") {
		t.Fatalf("expected truncation note in report, got: %s", output)
	}
	if strings.Contains(output, "code symbols absent") {
		t.Fatalf("truncated scan must not report broken symbols, got: %s", output)
	}
}
