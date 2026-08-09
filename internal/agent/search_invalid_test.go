package agent

import (
	"strings"
	"testing"
)

func TestSearchInvalidation_BasicDetection(t *testing.T) {
	s := newSearchInvalidationState()

	// Simulate a grep result referencing a file.
	s.recordSearchResult("grep", "/path/to/file.go:42: some content here")

	// Editing that file should trigger the invalidation warning.
	msg := s.checkEditInvalidation("/path/to/file.go")
	if msg == "" {
		t.Fatal("expected invalidation warning when editing file that appeared in grep results")
	}
	if !strings.Contains(msg, "STALE") {
		t.Errorf("warning should mention STALE, got: %s", msg)
	}
	if !strings.Contains(msg, "grep") {
		t.Errorf("warning should mention the source tool (grep), got: %s", msg)
	}
}

func TestSearchInvalidation_NoPriorSearch(t *testing.T) {
	s := newSearchInvalidationState()

	// Edit a file that was never referenced in any search.
	msg := s.checkEditInvalidation("/path/to/other.go")
	if msg != "" {
		t.Errorf("should not warn when file was not in any search result, got: %s", msg)
	}
}

func TestSearchInvalidation_DedupWarning(t *testing.T) {
	s := newSearchInvalidationState()

	s.recordSearchResult("grep", "/a/b.go:1: content")

	// First edit triggers warning.
	msg1 := s.checkEditInvalidation("/a/b.go")
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second edit of same file should not warn again.
	msg2 := s.checkEditInvalidation("/a/b.go")
	if msg2 != "" {
		t.Errorf("should not warn again for same file, got: %s", msg2)
	}
}

func TestSearchInvalidation_MaxWarnings(t *testing.T) {
	s := newSearchInvalidationState()

	// Record 5 files in search results.
	s.recordSearchResult("grep", `
/a/f1.go:1: c
/a/f2.go:2: c
/a/f3.go:3: c
/a/f4.go:4: c
/a/f5.go:5: c
`)

	// Only maxSearchInvalidationWarnings (3) should fire.
	count := 0
	for i := 1; i <= 5; i++ {
		path := "/a/f" + string(rune('0'+i)) + ".go"
		msg := s.checkEditInvalidation(path)
		if msg != "" {
			count++
		}
	}
	if count != maxSearchInvalidationWarnings {
		t.Errorf("expected %d warnings, got %d", maxSearchInvalidationWarnings, count)
	}
}

func TestSearchInvalidation_NonSearchToolIgnored(t *testing.T) {
	s := newSearchInvalidationState()

	// read_file output should not be tracked (only search-type tools).
	s.recordSearchResult("read_file", "/a/b.go:1: content")

	msg := s.checkEditInvalidation("/a/b.go")
	if msg != "" {
		t.Errorf("read_file should not trigger search invalidation, got: %s", msg)
	}
}

func TestSearchInvalidation_LSPDiagnosticsFormat(t *testing.T) {
	s := newSearchInvalidationState()

	// lsp_diagnostics output with bare paths (no line:col prefix).
	s.recordSearchResult("lsp_diagnostics", `{"path":"/some/file.go","diagnostics":[...]}`)

	msg := s.checkEditInvalidation("/some/file.go")
	if msg == "" {
		t.Fatal("expected invalidation for lsp_diagnostics result")
	}
}

func TestSearchInvalidation_Reset(t *testing.T) {
	s := newSearchInvalidationState()

	s.recordSearchResult("grep", "/a/b.go:1: c")
	s.checkEditInvalidation("/a/b.go")
	if s.warningCount != 1 {
		t.Fatalf("expected 1 warning, got %d", s.warningCount)
	}

	s.reset()

	if len(s.searchResultFiles) != 0 || len(s.invalidatedWarned) != 0 || s.warningCount != 0 {
		t.Error("reset should clear all state")
	}
}

func TestSearchInvalidation_MultipleSearchResultsSameFile(t *testing.T) {
	s := newSearchInvalidationState()

	// Multiple search tools referencing same file - only first registered.
	s.recordSearchResult("grep", "/a/b.go:1: content")
	s.recordSearchResult("code_search", "/a/b.go:10: other")

	msg := s.checkEditInvalidation("/a/b.go")
	if msg == "" {
		t.Fatal("expected warning")
	}
	// Should reference the first tool that found it.
	if !strings.Contains(msg, "grep") {
		t.Errorf("expected grep to be the source tool, got: %s", msg)
	}
}

func TestIsValidFilePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/a/b.go", true},
		{"./pkg/file.ts", true},
		{"a/b/c.py", true},
		{"ab", false},      // too short, no separator or dot
		{"", false},        // empty
		{"nodot", false},   // no dot
		{"/noext", false},  // no dot
		{"file.go", false}, // no slash (not absolute or relative path)
	}
	for _, tt := range tests {
		got := isValidFilePath(tt.path)
		if got != tt.want {
			t.Errorf("isValidFilePath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestSearchInvalidation_PathExtraction(t *testing.T) {
	s := newSearchInvalidationState()

	// Simulate realistic grep output with multiple paths and line numbers.
	output := `
internal/agent/foo.go:42: func fooBar() {
internal/agent/bar.go:17: var x = 42
pkg/util/baz.go:8: type Baz struct {
`
	s.recordSearchResult("grep", output)

	// All three files should be tracked.
	for _, p := range []string{"internal/agent/foo.go", "internal/agent/bar.go", "pkg/util/baz.go"} {
		if _, ok := s.searchResultFiles[normalizePath(p)]; !ok {
			t.Errorf("file %s should be tracked after grep result", p)
		}
	}
}
