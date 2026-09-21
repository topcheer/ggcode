package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsProbeableIdent(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ScanStaleness", true},  // camelCase
		{"my_func_name", true},   // snake_case
		{"item2Name", true},      // digit
		{"String", false},        // too short
		{"Window", false},        // no camel/snake/digit
		{"Error", false},         // too short
		{"HTTPServer", false},    // no lower→upper transition, no snake/digit
		{"abc", false},           // too short
		{"", false},              // empty
		{"has space", false},     // rejected by regexp anyway
		{"aCamelCaseName", true}, // camelCase
		{"ALLCAPS_NAME", true},   // snake_case constant
		{"alllower_name", true},  // snake_case
		{"with-dash", false},     // rejected: dash not identifier
	}
	for _, c := range cases {
		if got := isProbeableIdent(c.in); got != c.want {
			t.Errorf("isProbeableIdent(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestExtractSymbolCandidates(t *testing.T) {
	content := "Extend `ScanStaleness` via `ScanStaleness` helper; see `foo` and `Window` and `curateEntries`."
	ids := extractSymbolCandidates(content)
	want := []string{"ScanStaleness", "curateEntries"}
	if len(ids) != len(want) {
		t.Fatalf("expected %v, got %v", want, ids)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("candidate[%d] = %q, want %q", i, ids[i], id)
		}
	}
	if got := extractSymbolCandidates("no backticks here"); len(got) != 0 {
		t.Errorf("expected no candidates, got %v", got)
	}
}

func TestExtractSymbolCandidatesCap(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxProbeIdentsPerEntry+10; i++ {
		sb.WriteString("`ident_" + string(rune('a'+i%26)) + string(rune('0'+i%10)) + "Name1` ")
	}
	ids := extractSymbolCandidates(sb.String())
	if len(ids) != maxProbeIdentsPerEntry {
		t.Fatalf("expected cap %d, got %d", maxProbeIdentsPerEntry, len(ids))
	}
}

func TestIdentInFileBoundaries(t *testing.T) {
	content := "package x\n\nfunc RenderPage() {}\nfunc RenderX() {}\n"
	if !identInFile(content, "RenderPage") {
		t.Error("expected RenderPage to be found")
	}
	if identInFile(content, "Render") {
		t.Error("Render must not match inside RenderPage/RenderX (boundary check)")
	}
	if identInFile(content, "MissingSym") {
		t.Error("MissingSym should not be found")
	}
}

func TestScanStalenessBrokenSymbol(t *testing.T) {
	dir := t.TempDir()
	workingDir := t.TempDir()

	// Workspace contains one of the two referenced symbols.
	codeFile := filepath.Join(workingDir, "main.go")
	os.WriteFile(codeFile, []byte("package main\n\nfunc LivingSymbolName() {}\n"), 0644)

	am := &AutoMemory{dir: dir}
	am.SaveMemory("probe-mixed", "Wrap `LivingSymbolName` with `DeletedSymbolName` for retries.")

	report := am.ScanStaleness(workingDir)
	if report.BrokenSymbols != 1 {
		t.Fatalf("expected 1 broken-symbol finding, got %d", report.BrokenSymbols)
	}
	if report.ProbedFiles == 0 {
		t.Error("expected workspace files to be scanned")
	}
	var finding *StaleFinding
	for i := range report.Findings {
		if report.Findings[i].Reason == "broken-symbol" {
			finding = &report.Findings[i]
		}
	}
	if finding == nil {
		t.Fatal("expected a broken-symbol finding")
	}
	if finding.Key != "probe-mixed" {
		t.Errorf("finding key = %q, want probe-mixed", finding.Key)
	}
	if !strings.Contains(finding.Detail, "DeletedSymbolName") || strings.Contains(finding.Detail, "LivingSymbolName") {
		t.Errorf("detail should list only the missing symbol, got %q", finding.Detail)
	}
}

func TestScanStalenessSymbolProbeSkippedWithoutWorkingDir(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}
	am.SaveMemory("no-probe", "See `AbsentSymbolName` for details.")

	report := am.ScanStaleness("")
	if report.BrokenSymbols != 0 || report.ProbedFiles != 0 {
		t.Errorf("probe must be skipped without workingDir, got symbols=%d files=%d",
			report.BrokenSymbols, report.ProbedFiles)
	}
}

func TestScanStalenessAllSymbolsFound(t *testing.T) {
	dir := t.TempDir()
	workingDir := t.TempDir()

	codeFile := filepath.Join(workingDir, "pkg", "util.go")
	os.MkdirAll(filepath.Dir(codeFile), 0755)
	os.WriteFile(codeFile, []byte("package pkg\n\nfunc AlphaBetaName() {}\n"), 0644)

	am := &AutoMemory{dir: dir}
	am.SaveMemory("probe-ok", "Reuse `AlphaBetaName` when refactoring.")

	report := am.ScanStaleness(workingDir)
	if report.BrokenSymbols != 0 {
		t.Errorf("expected 0 broken-symbol findings, got %d", report.BrokenSymbols)
	}
	if report.ProbeTruncated {
		t.Error("complete scan must not report truncation")
	}
}

func TestProbeWorkspaceIdentsTruncationSignal(t *testing.T) {
	// #2621: every cap-driven SkipAll must surface a truncated signal so
	// callers can tell "confirmed absent" from "never scanned".
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc alphaOne() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package b\nfunc betaTwo() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// File cap fires before any file is read.
	oldFiles := maxProbeFiles
	oldBytes := maxProbeTotalBytes
	defer func() {
		maxProbeFiles = oldFiles
		maxProbeTotalBytes = oldBytes
	}()
	maxProbeFiles = 0
	found, scanned, truncated := probeWorkspaceIdents(dir, map[string]struct{}{"alphaOne": {}})
	maxProbeFiles = oldFiles
	if !truncated || scanned != 0 || found["alphaOne"] {
		t.Fatalf("file cap: truncated=%v scanned=%d found=%v", truncated, scanned, found)
	}

	// Byte cap fires on the first file, so sub/b.go is never scanned.
	maxProbeTotalBytes = 1
	found, _, truncated = probeWorkspaceIdents(dir, map[string]struct{}{"betaTwo": {}})
	maxProbeTotalBytes = oldBytes
	if !truncated || found["betaTwo"] {
		t.Fatalf("byte cap: truncated=%v found=%v", truncated, found)
	}

	// Early exit once every identifier is found is success, not truncation.
	found, _, truncated = probeWorkspaceIdents(dir, map[string]struct{}{"alphaOne": {}})
	if truncated || !found["alphaOne"] {
		t.Fatalf("complete scan: truncated=%v found=%v", truncated, found)
	}
}

func TestScanStalenessTruncatedProbeSkipsBrokenSymbol(t *testing.T) {
	// #2621 regression: a cap-truncated probe must not record
	// broken-symbol findings for identifiers that were simply never
	// scanned (the symbol lives in a lexically later directory).
	workingDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workingDir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workingDir, "z"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "a", "filler.go"), []byte("package a\nfunc fillerThing() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workingDir, "z", "real.go"), []byte("package z\nfunc realSymbolName() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	am := &AutoMemory{dir: t.TempDir()}
	am.SaveMemory("probe-cap", "Always call `realSymbolName` before exit.")

	oldBytes := maxProbeTotalBytes
	maxProbeTotalBytes = 1 // a/filler.go alone blows the cap; z/ is never reached
	defer func() { maxProbeTotalBytes = oldBytes }()

	report := am.ScanStaleness(workingDir)
	if !report.ProbeTruncated {
		t.Fatal("expected ProbeTruncated=true when the byte cap stops the walk")
	}
	if report.BrokenSymbols != 0 {
		t.Fatalf("truncated scan must not emit broken-symbol findings, got %d", report.BrokenSymbols)
	}
	for _, f := range report.Findings {
		if f.Reason == "broken-symbol" {
			t.Fatalf("truncated scan emitted a broken-symbol finding for %s", f.Key)
		}
	}
}
