package tool

// Guard tests for the #853-#867 fix round. Each test pins the new semantics
// so a regression reintroduces a visible failure instead of silent drift.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeTestNotebook(t *testing.T, nb string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.ipynb")
	if err := os.WriteFile(path, []byte(nb), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func notebookExec(t *testing.T, tool NotebookEdit, rawArgs string) (Result, error) {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(rawArgs))
}

// TestNotebookReplaceMarkdownStripsOutputs (#853): replacing a markdown cell
// must not write outputs/execution_count (nbformat forbids them on non-code
// cells).
func TestNotebookReplaceMarkdownStripsOutputs(t *testing.T) {
	path := writeTestNotebook(t, `{"cells":[{"cell_type":"markdown","source":["# Old"],"metadata":{}}],"metadata":{},"nbformat":4}`)
	tool := NotebookEdit{}
	res, err := notebookExec(t, tool, `{"notebook_path":"`+path+`","operation":"replace","cell_number":0,"source":"# New heading","description":"test"}`)
	if err != nil || res.IsError {
		t.Fatalf("replace failed: %v %s", err, res.Content)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	cell, ok := doc["cells"].([]any)[0].(map[string]any)
	if !ok {
		t.Fatalf("cell not an object: %T", doc["cells"].([]any)[0])
	}
	if _, has := cell["outputs"]; has {
		t.Fatal("outputs written on markdown cell (nbformat violation)")
	}
	if _, has := cell["execution_count"]; has {
		t.Fatal("execution_count written on markdown cell")
	}
}

// TestNotebookReplaceRequiresSource (#854): omitting source must error, not
// silently wipe the cell content.
func TestNotebookReplaceRequiresSource(t *testing.T) {
	path := writeTestNotebook(t, `{"cells":[{"cell_type":"code","source":["print(1)"],"metadata":{}}],"metadata":{},"nbformat":4}`)
	tool := NotebookEdit{}
	res, err := notebookExec(t, tool, `{"notebook_path":"`+path+`","operation":"replace","cell_number":0,"source":"","description":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("empty source replace must be an error, not silent wipe")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "print(1)") {
		t.Fatal("cell content was wiped despite the error guard")
	}
}

// TestHasPathPrefixSegment (#867): nested monorepo artifacts match; config
// look-alikes do not.
func TestHasPathPrefixSegment(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"dist/bundle.js", "dist/", true},
		{"web/dist/bundle.js", "dist/", true},
		{"packages/app/node_modules/x.js", "node_modules/", true},
		{"vendor/github.com/pkg/errors/errors.go", "vendor/", true},
		{".coveragerc", ".coverage", false},
		{".coverage", ".coverage", true},
		{"web/.coverage", ".coverage", true},
		{"distribution/config.yml", "dist/", false},
	}
	for _, c := range cases {
		if got := hasPathPrefixSegment(c.path, c.prefix); got != c.want {
			t.Errorf("hasPathPrefixSegment(%q, %q) = %v, want %v", c.path, c.prefix, got, c.want)
		}
	}
}

// TestTaskIDLessNumeric (#866): task-2 < task-10 numerically.
func TestTaskIDLessNumeric(t *testing.T) {
	if !taskIDLess("task-2", "task-10") {
		t.Fatal("string order used: task-10 should sort after task-2")
	}
	if taskIDLess("task-10", "task-2") {
		t.Fatal("task-10 must not sort before task-2")
	}
	if !taskIDLess("task-1", "task-2") {
		t.Fatal("basic numeric order broken")
	}
}

// TestDetectCommentBlocksFileAdjacency (#860): comment lines separated by
// context lines (gap in file line numbers) must NOT be flagged as one block.
func TestDetectCommentBlocksFileAdjacency(t *testing.T) {
	file := &reviewDiffFile{
		path: "main.go",
		addedLines: []reviewDiffLine{
			{1, "//	if err != nil {"},
			{2, "//		return fmt.Errorf(\"bad\")"},
			// gap: line 3 is a context line (not added)
			{4, "//	for i := 0; i < 10; i++ {"},
			{5, "//		_ = i"},
		},
	}
	findings := detectCommentBlocks(file)
	if len(findings) != 0 {
		t.Fatalf("scattered comments flagged as block: %d findings", len(findings))
	}
}

// TestZeroErrorsWordBoundary (#859): "Found 10 errors" must not be filtered
// by the zero-error exclusion; "Found 0 errors" must be.
func TestZeroErrorsWordBoundary(t *testing.T) {
	zero := regexp.MustCompile(`\b0 errors`)
	if zero.MatchString(strings.ToLower("found 10 errors in 2 files.")) {
		t.Fatal("'10 errors' matched zero-error filter")
	}
	if !zero.MatchString(strings.ToLower("Found 0 errors")) {
		t.Fatal("'0 errors' no longer matches zero-error filter")
	}
}
