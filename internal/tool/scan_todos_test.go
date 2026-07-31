package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanTodos_BasicScan(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "example.go")
	os.WriteFile(testFile, []byte(`package example

// TODO: implement this function
func foo() {
	// FIXME: this is broken
}

// HACK: temporary workaround for issue #42
func bar() {
	// NOTE: remember to update this
}
`), 0644)

	cleanFile := filepath.Join(tmpDir, "clean.go")
	os.WriteFile(cleanFile, []byte("package example\n\nfunc clean() {}\n"), 0644)

	tool := ScanTodosTool{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{"description": "test scan"})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}

	for _, marker := range []string{"TODO", "FIXME", "HACK", "NOTE"} {
		if !strings.Contains(result.Content, marker) {
			t.Errorf("expected %s in results", marker)
		}
	}
	if !strings.Contains(result.Content, "Total Markers: 4") {
		t.Errorf("expected 4 markers, content: %s", result.Content)
	}
}

func TestScanTodos_CategoryFilter(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "example.go")
	os.WriteFile(testFile, []byte(`package example

// TODO: implement this
// FIXME: fix this
// HACK: hack this
`), 0644)

	tool := ScanTodosTool{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{
		"description": "test filter",
		"categories":  "TODO",
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	if !strings.Contains(result.Content, "Total Markers: 1") {
		t.Errorf("expected 1 marker with TODO filter, got: %s", result.Content)
	}
}

func TestScanTodos_NoMarkers(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "clean.go")
	os.WriteFile(testFile, []byte("package clean\n\nfunc main() {}\n"), 0644)

	tool := ScanTodosTool{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{"description": "test clean"})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "No TODO") {
		t.Errorf("expected 'No TODO' message, got: %s", result.Content)
	}
}

func TestScanTodos_SkipDirs(t *testing.T) {
	tmpDir := t.TempDir()

	vendorDir := filepath.Join(tmpDir, "vendor")
	os.MkdirAll(vendorDir, 0755)
	vendorFile := filepath.Join(vendorDir, "vendor.go")
	os.WriteFile(vendorFile, []byte("// TODO: should be skipped\npackage vendor\n"), 0644)

	regularFile := filepath.Join(tmpDir, "main.go")
	os.WriteFile(regularFile, []byte("// TODO: should be found\npackage main\n"), 0644)

	tool := ScanTodosTool{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]string{"description": "test skip dirs"})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	if !strings.Contains(result.Content, "Total Markers: 1") {
		t.Errorf("expected 1 marker (vendor skipped), got: %s", result.Content)
	}
}

func TestScanTodos_MaxResults(t *testing.T) {
	tmpDir := t.TempDir()

	content := "package main\n\n"
	for i := 0; i < 10; i++ {
		content += "// TODO: task\n"
	}

	testFile := filepath.Join(tmpDir, "many.go")
	os.WriteFile(testFile, []byte(content), 0644)

	tool := ScanTodosTool{WorkingDir: tmpDir}
	input, _ := json.Marshal(map[string]interface{}{
		"description": "test max results",
		"max_results": 3,
	})

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	if !strings.Contains(result.Content, "Total Markers: 10") {
		t.Errorf("expected total 10, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "showing top 3") {
		t.Errorf("expected truncated to 3, got: %s", result.Content)
	}
}

func TestExtractTodoMarker(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
		found    bool
	}{
		{"simple TODO", "// TODO: implement this", "TODO", true},
		{"FIXME inline", "// FIXME: broken", "FIXME", true},
		{"lowercase todo", "// todo: lowercase", "TODO", true},
		{"no marker", "// just a comment", "", false},
		{"word boundary", "// TODOLIST: not a marker", "", false},
		{"WORKAROUND", "// WORKAROUND: for bug 123", "WORKAROUND", true},
		{"BUG", "// BUG: crash on nil ptr", "BUG", true},
		{"XXX", "// XXX: needs review", "XXX", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := extractTodoMarker("test.go", 1, tt.line, nil)
			if tt.found {
				if m == nil {
					t.Fatalf("expected marker %s, got nil", tt.expected)
				}
				if m.Category != tt.expected {
					t.Errorf("expected category %s, got %s", tt.expected, m.Category)
				}
			} else {
				if m != nil {
					t.Errorf("expected no marker, got %s", m.Category)
				}
			}
		})
	}
}

func TestScanTodos_Clone(t *testing.T) {
	tool := ScanTodosTool{WorkingDir: "/test/path"}
	cloned := tool.Clone()
	scanClone, ok := cloned.(*ScanTodosTool)
	if !ok {
		t.Fatalf("expected *ScanTodosTool, got %T", cloned)
	}
	if scanClone.WorkingDir != "/test/path" {
		t.Errorf("expected WorkingDir /test/path, got %s", scanClone.WorkingDir)
	}
}

func TestFormatAge(t *testing.T) {
	// Recent (within a day)
	recent := time.Now().Add(-2 * time.Hour)
	age := formatAge(recent)
	if age != "today" && !strings.Contains(age, "d") {
		t.Errorf("expected 'today' or 'Nd', got %s", age)
	}

	// 10 days old
	old := time.Now().Add(-10 * 24 * time.Hour)
	age = formatAge(old)
	if !strings.Contains(age, "d") {
		t.Errorf("expected days, got %s", age)
	}

	// 60 days old (should show months)
	older := time.Now().Add(-60 * 24 * time.Hour)
	age = formatAge(older)
	if !strings.Contains(age, "mo") {
		t.Errorf("expected months, got %s", age)
	}

	// 400 days old (should show years)
	ancient := time.Now().Add(-400 * 24 * time.Hour)
	age = formatAge(ancient)
	if !strings.Contains(age, "y") {
		t.Errorf("expected years, got %s", age)
	}
}

func TestFindMarkerIndex(t *testing.T) {
	tests := []struct {
		line        string
		marker      string
		expectFound bool
	}{
		{"// TODO: stuff", "TODO", true},
		{"// TODOLIST: stuff", "TODO", false},
		{"func TODOThing()", "TODO", false},
		{"// FIXME:", "FIXME", true},
		{"// fixme:", "FIXME", true},
	}

	for _, tt := range tests {
		idx := findMarkerIndex(strings.ToUpper(tt.line), tt.marker)
		if tt.expectFound && idx < 0 {
			t.Errorf("expected to find %s in %q", tt.marker, tt.line)
		}
		if !tt.expectFound && idx >= 0 {
			t.Errorf("expected NOT to find %s in %q (got idx %d)", tt.marker, tt.line, idx)
		}
	}
}
