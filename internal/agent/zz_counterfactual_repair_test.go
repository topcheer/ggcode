package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCounterfactualRepairClassifiesToolClass(t *testing.T) {
	cases := []struct {
		tool   string
		substr string
	}{
		{"edit_file", "re-read the file immediately"},
		{"multi_file_write", "re-read the file immediately"},
		{"run_command", "full error output"},
		{"start_command", "full error output"},
		{"browser_navigate", "snapshot the DOM"},
		{"go_test_runner", "-run first"}, // test-class via fold
		{"search_files", "narrow the query"},
		{"lsp_references", "re-resolve the symbol"},
		{"something_unknown", "change approach"},
	}
	for _, c := range cases {
		got := CounterfactualRepairStep(c.tool)
		if !strings.Contains(got, c.substr) {
			t.Errorf("repair(%q)=%q want substring %q", c.tool, got, c.substr)
		}
	}
}

func TestRecordFailureAttributionWritesRepair(t *testing.T) {
	dir := t.TempDir()
	RecordFailureAttribution(dir, "fix the failing test suite", "run_command", "/w/main.go", 7)
	entries := loadFailureEntries(filepath.Join(dir, ".ggcode", "playbook_failures.json"))
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	if entries[0].Repair != CounterfactualRepairStep("run_command") {
		t.Fatalf("repair not persisted: %+v", entries[0])
	}
}

func TestFailureHintsForPromptIncludesRepair(t *testing.T) {
	dir := t.TempDir()
	RecordFailureAttribution(dir, "fix the failing test suite", "edit_file", "/w/auth.go", 5)
	hint := FailureHintsForPrompt(dir, "fix the failing test suite", 5)
	if !strings.Contains(hint, "fix: re-read the file immediately") {
		t.Fatalf("hint must carry repair directive:\n%s", hint)
	}
}
