package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/lsp"
)

func TestDiffAgainstBaseline_NoBaseline(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Severity: 1, Message: "undefined: foo", Range: lsp.Range{Start: lsp.Position{Line: 10}}},
	}
	newDiags, resolved, hasBaseline := diffAgainstBaseline("/nonexistent/file.go", diags)
	if hasBaseline {
		t.Error("expected hasBaseline=false when no baseline stored")
	}
	// When no baseline exists, all current diags are "new"
	if len(newDiags) != 1 {
		t.Errorf("expected 1 new diag, got %d", len(newDiags))
	}
	if resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", resolved)
	}
}

func TestDiffAgainstBaseline_AllPreExisting(t *testing.T) {
	path := "/test/preexisting.go"
	baseline := []lsp.Diagnostic{
		{Severity: 1, Message: "unused variable: x", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Severity: 2, Message: "inefficient assignment", Range: lsp.Range{Start: lsp.Position{Line: 10}}},
	}

	// Store baseline directly
	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{
		counts: newDiagCounts(baseline),
		total:  len(baseline),
	}
	diagBaselineMu.Unlock()

	// Same diagnostics post-edit (different line numbers due to insertion)
	postEdit := []lsp.Diagnostic{
		{Severity: 1, Message: "unused variable: x", Range: lsp.Range{Start: lsp.Position{Line: 7}}},
		{Severity: 2, Message: "inefficient assignment", Range: lsp.Range{Start: lsp.Position{Line: 12}}},
	}

	newDiags, resolved, hasBaseline := diffAgainstBaseline(path, postEdit)
	if !hasBaseline {
		t.Fatal("expected hasBaseline=true")
	}
	if len(newDiags) != 0 {
		t.Errorf("expected 0 new diags (all pre-existing), got %d: %+v", len(newDiags), newDiags)
	}
	if resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", resolved)
	}
}

func TestDiffAgainstBaseline_NewIssuesIntroduced(t *testing.T) {
	path := "/test/newissue.go"
	baseline := []lsp.Diagnostic{
		{Severity: 1, Message: "pre-existing error", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
	}

	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{
		counts: newDiagCounts(baseline),
		total:  1,
	}
	diagBaselineMu.Unlock()

	postEdit := []lsp.Diagnostic{
		{Severity: 1, Message: "pre-existing error", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Severity: 1, Message: "undefined: newVar", Range: lsp.Range{Start: lsp.Position{Line: 20}}},
		{Severity: 2, Message: "unused: y", Range: lsp.Range{Start: lsp.Position{Line: 25}}},
	}

	newDiags, resolved, hasBaseline := diffAgainstBaseline(path, postEdit)
	if !hasBaseline {
		t.Fatal("expected hasBaseline=true")
	}
	if len(newDiags) != 2 {
		t.Errorf("expected 2 new diags, got %d", len(newDiags))
	}
	if resolved != 0 {
		t.Errorf("expected 0 resolved, got %d", resolved)
	}
}

func TestDiffAgainstBaseline_ResolvedIssues(t *testing.T) {
	path := "/test/resolved.go"
	baseline := []lsp.Diagnostic{
		{Severity: 1, Message: "error A", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Severity: 2, Message: "warning B", Range: lsp.Range{Start: lsp.Position{Line: 10}}},
	}

	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{
		counts: newDiagCounts(baseline),
		total:  2,
	}
	diagBaselineMu.Unlock()

	// Post-edit: both issues are gone
	postEdit := []lsp.Diagnostic{}

	newDiags, resolved, hasBaseline := diffAgainstBaseline(path, postEdit)
	if !hasBaseline {
		t.Fatal("expected hasBaseline=true")
	}
	if len(newDiags) != 0 {
		t.Errorf("expected 0 new diags, got %d", len(newDiags))
	}
	if resolved != 2 {
		t.Errorf("expected 2 resolved, got %d", resolved)
	}
}

func TestDiffAgainstBaseline_BaselineConsumed(t *testing.T) {
	path := "/test/consumed.go"
	baseline := []lsp.Diagnostic{
		{Severity: 1, Message: "error A", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
	}

	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{
		counts: newDiagCounts(baseline),
		total:  1,
	}
	diagBaselineMu.Unlock()

	diffAgainstBaseline(path, baseline)

	// Second call should find no baseline (consumed)
	_, _, hasBaseline := diffAgainstBaseline(path, baseline)
	if hasBaseline {
		t.Error("expected baseline to be consumed after first diff")
	}
}

func TestDiffAgainstBaseline_DuplicateDiagnostics(t *testing.T) {
	path := "/test/dup.go"
	// File has 2 "unused variable" warnings pre-edit
	baseline := []lsp.Diagnostic{
		{Severity: 2, Message: "unused variable: a", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Severity: 2, Message: "unused variable: a", Range: lsp.Range{Start: lsp.Position{Line: 6}}},
	}

	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{
		counts: newDiagCounts(baseline),
		total:  2,
	}
	diagBaselineMu.Unlock()

	// Post-edit: 3 "unused variable: a" warnings — one is new
	postEdit := []lsp.Diagnostic{
		{Severity: 2, Message: "unused variable: a", Range: lsp.Range{Start: lsp.Position{Line: 5}}},
		{Severity: 2, Message: "unused variable: a", Range: lsp.Range{Start: lsp.Position{Line: 6}}},
		{Severity: 2, Message: "unused variable: a", Range: lsp.Range{Start: lsp.Position{Line: 20}}},
	}

	newDiags, _, hasBaseline := diffAgainstBaseline(path, postEdit)
	if !hasBaseline {
		t.Fatal("expected hasBaseline=true")
	}
	if len(newDiags) != 1 {
		t.Errorf("expected 1 new diag (third duplicate), got %d", len(newDiags))
	}
}

func TestFormatNewDiagnostics_WithNewErrors(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Severity: 1, Message: "undefined: foo", Range: lsp.Range{Start: lsp.Position{Line: 9}}},
		{Severity: 2, Message: "unused: bar", Range: lsp.Range{Start: lsp.Position{Line: 14}}},
	}
	result := formatNewDiagnostics(diags, 0)
	if !strings.Contains(result, "[New Diagnostics") {
		t.Error("expected 'New Diagnostics' header")
	}
	if !strings.Contains(result, "undefined: foo") {
		t.Error("expected new error message")
	}
	if !strings.Contains(result, "unused: bar") {
		t.Error("expected new warning message")
	}
}

func TestFormatNewDiagnostics_OnlyResolved(t *testing.T) {
	result := formatNewDiagnostics(nil, 3)
	if !strings.Contains(result, "resolved") {
		t.Errorf("expected 'resolved' message, got: %s", result)
	}
	if strings.Contains(result, "New Diagnostics") {
		t.Error("should not show New Diagnostics header when no new issues")
	}
}

func TestFormatNewDiagnostics_Empty(t *testing.T) {
	result := formatNewDiagnostics(nil, 0)
	if result != "" {
		t.Errorf("expected empty string for no diags, got: %s", result)
	}
}

func TestClearDiagnosticBaseline(t *testing.T) {
	path := "/test/clear.go"
	diagBaselineMu.Lock()
	diagBaselines[path] = baselineSnapshot{counts: diagCounts{}, total: 1}
	diagBaselineMu.Unlock()

	ClearDiagnosticBaseline(path)

	diagBaselineMu.Lock()
	_, exists := diagBaselines[path]
	diagBaselineMu.Unlock()
	if exists {
		t.Error("expected baseline to be cleared")
	}
}

func TestNewDiagCounts(t *testing.T) {
	diags := []lsp.Diagnostic{
		{Severity: 1, Message: "error A"},
		{Severity: 1, Message: "error A"}, // duplicate
		{Severity: 2, Message: "warning B"},
	}
	dc := newDiagCounts(diags)
	if dc[diagEntry{severity: 1, message: "error A"}] != 2 {
		t.Error("expected count 2 for duplicate error A")
	}
	if dc[diagEntry{severity: 2, message: "warning B"}] != 1 {
		t.Error("expected count 1 for warning B")
	}
}
